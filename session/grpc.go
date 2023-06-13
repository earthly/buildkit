package session

import (
	"context"
	"fmt"
	"math"
	"net"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/defaults"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/grpcerrors"
)

func serve(ctx context.Context, grpcServer *grpc.Server, conn net.Conn) {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	bklog.G(ctx).Debugf("serving grpc connection")
	(&http2.Server{}).ServeConn(conn, &http2.ServeConnOpts{Handler: grpcServer})
}

func unaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		fmt.Println("unary middleware")
		ctx, _ = context.WithTimeout(ctx, 10*time.Second)
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			return err
		}
		return nil
	}
}

func streamInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx, _ = context.WithTimeout(ctx, 10*time.Second)
		fmt.Println("stream middleware")
		newStreamer, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			return newStreamer, err
		}
		return newStreamer, nil
	}
}

func grpcClientConn(ctx context.Context, conn net.Conn, healthCfg ManagerHealthCfg) (context.Context, *grpc.ClientConn, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	var unary []grpc.UnaryClientInterceptor
	var stream []grpc.StreamClientInterceptor

	var dialCount int64
	dialer := grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		if c := atomic.AddInt64(&dialCount, 1); c > 1 {
			return nil, errors.Errorf("only one connection allowed")
		}
		return conn, nil
	})

	dialOpts := []grpc.DialOption{
		dialer,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(65535 * 32),
		grpc.WithInitialConnWindowSize(65535 * 16),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
		//grpc.WithChainStreamInterceptor(streamInterceptor()),
		//grpc.WithChainUnaryInterceptor(unaryInterceptor()),
	}
	unary = append(unary, unaryInterceptor())
	stream = append(stream, streamInterceptor())

	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		unary = append(unary, filterClient(otelgrpc.UnaryClientInterceptor(otelgrpc.WithTracerProvider(span.TracerProvider()), otelgrpc.WithPropagators(propagators))))
		stream = append(stream, otelgrpc.StreamClientInterceptor(otelgrpc.WithTracerProvider(span.TracerProvider()), otelgrpc.WithPropagators(propagators)))
	}

	unary = append(unary, grpcerrors.UnaryClientInterceptor)
	stream = append(stream, grpcerrors.StreamClientInterceptor)

	if len(unary) == 1 {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(unary[0]))
	} else if len(unary) > 1 {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(grpc_middleware.ChainUnaryClient(unary...)))
	}

	if len(stream) == 1 {
		dialOpts = append(dialOpts, grpc.WithStreamInterceptor(stream[0]))
	} else if len(stream) > 1 {
		dialOpts = append(dialOpts, grpc.WithStreamInterceptor(grpc_middleware.ChainStreamClient(stream...)))
	}

	cc, err := grpc.DialContext(ctx, "localhost", dialOpts...)
	if err != nil {
		cancel()
		return nil, nil, nil, errors.Wrap(err, "failed to create grpc client")
	}

	//go configurableMonitorHealth(ctx, cc, cancel, healthCfg)

	return ctx, cc, func() { fmt.Println("cancel"); cancel() }, nil
}

func monitorHealth(ctx context.Context, cc *grpc.ClientConn, cancelConn func()) {
	defer cancelConn()
	defer cc.Close()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	healthClient := grpc_health_v1.NewHealthClient(cc)

	failedBefore := false
	consecutiveSuccessful := 0
	defaultHealthcheckDuration := 30 * time.Second
	lastHealthcheckDuration := time.Duration(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// This healthcheck can erroneously fail in some instances, such as receiving lots of data in a low-bandwidth scenario or too many concurrent builds.
			// So, this healthcheck is purposely long, and can tolerate some failures on purpose.

			healthcheckStart := time.Now()

			timeout := time.Duration(math.Max(float64(defaultHealthcheckDuration), float64(lastHealthcheckDuration)*1.5))
			ctx, cancel := context.WithTimeout(ctx, timeout)
			_, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
			cancel()

			lastHealthcheckDuration = time.Since(healthcheckStart)
			logFields := logrus.Fields{
				"timeout":        timeout,
				"actualDuration": lastHealthcheckDuration,
			}

			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if failedBefore {
					bklog.G(ctx).Error("healthcheck failed fatally")
					return
				}

				failedBefore = true
				consecutiveSuccessful = 0
				bklog.G(ctx).WithFields(logFields).Warn("healthcheck failed")
			} else {
				consecutiveSuccessful++

				if consecutiveSuccessful >= 5 && failedBefore {
					failedBefore = false
					bklog.G(ctx).WithFields(logFields).Debug("reset healthcheck failure")
				}
			}

			bklog.G(ctx).WithFields(logFields).Trace("healthcheck completed")
		}
	}
}
