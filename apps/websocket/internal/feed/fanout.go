package feed

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var errFanoutStuck = errors.New("fanout workers did not drain in time")

type job struct {
	publish bool
	env     Envelope
}

type fanout struct {
	queues  []chan job
	handler Handler
	stop    chan struct{}
	halted  sync.Once
	mu      sync.RWMutex
	closed  bool
	skip    atomic.Bool
	done    sync.WaitGroup
}

func newFanout(workers, queue int, h Handler) *fanout {
	f := &fanout{queues: make([]chan job, workers), handler: h, stop: make(chan struct{})}
	for i := range f.queues {
		f.queues[i] = make(chan job, queue)
		f.done.Go(func() { f.work(f.queues[i]) })
	}
	return f
}

func (f *fanout) work(queue chan job) {
	for j := range queue {
		switch {
		case f.skip.Load():
		case j.publish:
			f.handler.Publish(j.env)
		default:
			f.handler.Event(j.env)
		}
	}
}

func (f *fanout) send(key string, j job) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return false
	}
	select {
	case f.queues[spread(key, len(f.queues))] <- j:
		return true
	case <-f.stop:
		return false
	}
}

func (f *fanout) halt() { f.halted.Do(func() { close(f.stop) }) }

func (f *fanout) depth() int {
	n := 0
	for _, q := range f.queues {
		n += len(q)
	}
	return n
}

func (f *fanout) close(ctx context.Context) error {
	f.halt()
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		for _, q := range f.queues {
			close(q)
		}
	}
	f.mu.Unlock()
	drained := make(chan struct{})
	go func() {
		f.done.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		f.skip.Store(true)
		return errFanoutStuck
	}
}
