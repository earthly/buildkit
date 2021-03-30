package retryhandler

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func New(f images.HandlerFunc, logger func([]byte)) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		backoff := time.Second
		for {
			descs, err := f(ctx, desc)
			if err != nil {
				select {
				case <-ctx.Done():
					return nil, err
				default:
					if !retryError(err) {
						if logger != nil {
							st := getStackTrace(err)
							logger([]byte(fmt.Sprintf("error: %v\nstack trace: %s\n", err.Error(), st)))
						}
						return nil, err
					}
				}
				if logger != nil {
					st := getStackTrace(err)
					logger([]byte(fmt.Sprintf("error: %v\nstack trace: %s\n", err.Error(), st)))
				}
			} else {
				return descs, nil
			}
			// backoff logic
			if backoff >= 8*time.Second {
				return nil, err
			}
			if logger != nil {
				logger([]byte(fmt.Sprintf("retrying in %v\n", backoff)))
			}
			time.Sleep(backoff)
			backoff *= 2
		}
	}
}

func retryError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	// catches TLS timeout or other network-related temporary errors.
	if ne, ok := errors.Cause(err).(net.Error); ok && ne.Temporary() {
		return true
	}
	// https://github.com/containerd/containerd/pull/4724
	if errors.Cause(err).Error() == "no response" {
		return true
	}

	// net.ErrClosed exposed in go1.16
	if strings.Contains(err.Error(), "use of closed network connection") {
		return true
	}

	return false
}

func getStackTrace(err error) string {
	type stackTracer interface {
		StackTrace() errors.StackTrace
	}
	errChain := []error{}
	for it := err; it != nil; it = errors.Unwrap(it) {
		errChain = append(errChain, it)
	}
	for index := len(errChain) - 1; index > 0; index-- {
		it := errChain[index]
		errWithStack, ok := it.(stackTracer)
		if ok {
			return fmt.Sprintf("%+v", errWithStack.StackTrace())
		}
	}
	return ""
}
