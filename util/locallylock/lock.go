package locallylock

import (
	"context"
	"sync"
)

type Lock struct {
	m       *sync.Mutex
	c       *sync.Cond
	current string
	ctx     context.Context
}

func New() *Lock {
	m := sync.Mutex{}
	c := sync.NewCond(&m)

	return &Lock{
		m: &m,
		c: c,
	}
}

func (l *Lock) Lock(ctx context.Context, name string) {
	l.m.Lock()
	for {
		if l.ctx != nil {
			select {
			case <-l.ctx.Done():
				// unlock locks from closed connections
				l.current = ""
			default:
			}
		}
		if l.current == "" || l.current == name {
			l.current = name
			l.ctx = ctx
			l.m.Unlock()
			return
		}
		l.c.Wait()
	}
}

func (l *Lock) Unlock() {
	l.m.Lock()
	l.current = ""
	l.ctx = nil
	l.m.Unlock()
	l.c.Broadcast()
}
