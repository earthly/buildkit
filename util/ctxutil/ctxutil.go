package ctxutil

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
)

// WithCancel is equivalent to context.WithCancel, but it also adds in a stack trace
// of where the WithCancel has been created.
func WithCancel(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	return newCtxWithStackTrace(ctx), cancel
}

// WithTimeout is equivalent to context.WithTimeout, but it also adds in a stack trace
// of where the WithTimeout has been created.
func WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	return newCtxWithStackTrace(ctx), cancel
}

type ctxWithStackTrace struct {
	ctx context.Context

	doneCh chan struct{}
	err    error
}

func newCtxWithStackTrace(parent context.Context) context.Context {
	c := ctxWithStackTrace{
		doneCh: make(chan struct{}),
		ctx:    parent,
	}
	canceledErr := errors.WithStack(context.Canceled)
	deadlineExceededErr := errors.WithStack(context.DeadlineExceeded)
	go func() {
		<-c.ctx.Done()
		ctxErr := c.ctx.Err()
		switch ctxErr {
		case context.Canceled:
			c.err = canceledErr
		case context.DeadlineExceeded:
			c.err = deadlineExceededErr
		default:
			// this would not include a stack trace (or it would include the stack trace of
			// the context that was done).
			c.err = ctxErr
		}
		close(c.doneCh)
	}()
	return c
}

func (c ctxWithStackTrace) Deadline() (time.Time, bool) {
	return c.ctx.Deadline()
}

func (c ctxWithStackTrace) Done() <-chan struct{} {
	return c.ctx.Done()
}

func (c ctxWithStackTrace) Err() error {
	select {
	case <-c.ctx.Done():
		// @# ret := c.ctx.Err()
		<-c.doneCh
		if c.err != nil {
			fmt.Printf("@#@#@# Err: %s Stack: %s\n", c.err.Error(), getStackTrace(c.err))
		}
		return c.err
	default:
	}
	return nil
}

func (c ctxWithStackTrace) Value(key interface{}) interface{} {
	return c.ctx.Value(key)
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
