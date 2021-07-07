package locallylock

import (
	"context"
	"sync"
	"testing"
)

func TestLock(t *testing.T) {
	l := New()
	wg := sync.WaitGroup{}
	seen := map[string]int{}

	looper := func(name string) {
		defer wg.Done()
		ctx := context.Background()
		l.Lock(ctx, name)
		before := len(seen)
		for i := 0; i < 100; i++ {
			l.Lock(ctx, name)
			seen[name]++
		}
		after := len(seen)
		l.Unlock()
		if (before + 1) != after {
			t.Error("out of order loops")
		}
	}

	wg.Add(3)
	go looper("a")
	go looper("b")
	go looper("c")

	wg.Wait()
	if len(seen) != 3 {
		t.Error("not all 3 loopers ran")
	}
}

func TestCanceledLock(t *testing.T) {
	l := New()

	ctx, cancel := context.WithCancel(context.Background())

	l.Lock(ctx, "a")

	cancel()

	ctx2 := context.Background()
	l.Lock(ctx2, "b")
	l.Unlock()

	l.Lock(ctx2, "c")
}
