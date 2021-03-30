package ctxutil

import (
	"context"
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
	ctx                 context.Context
	canceledErr         error
	deadlineExceededErr error
}

func newCtxWithStackTrace(parent context.Context) context.Context {
	c := ctxWithStackTrace{
		ctx:                 parent,
		canceledErr:         errors.WithStack(context.Canceled),
		deadlineExceededErr: errors.WithStack(context.DeadlineExceeded),
	}
	return c
}

func (c ctxWithStackTrace) Deadline() (time.Time, bool) {
	return c.ctx.Deadline()
}

func (c ctxWithStackTrace) Done() <-chan struct{} {
	return c.ctx.Done()
}

func (c ctxWithStackTrace) Err() error {
	ctxErr := c.ctx.Err()
	if errors.Is(ctxErr, context.Canceled) {
		return c.canceledErr
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return c.deadlineExceededErr
	}
	// this would not include a stack trace (or it would include the stack trace of
	// the context that was done).
	return ctxErr
}

func (c ctxWithStackTrace) Value(key interface{}) interface{} {
	return c.ctx.Value(key)
}
