package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

var (
	errFanoutStuck   = errors.New("fanout workers did not drain in time")
	errFanoutStopped = errors.New("fanout stopped")
)

type job struct {
	publish bool
	env     Envelope
	mark    *barrierMark
}

type barrierMark struct {
	left   atomic.Int64
	passed chan struct{}
}

func (m *barrierMark) pass() {
	if m.left.Add(-1) == 0 {
		close(m.passed)
	}
}

type fanout struct {
	queues  []chan job
	handler Handler
	log     *slog.Logger
	panics  atomic.Uint64
	stop    chan struct{}
	halted  sync.Once
	mu      sync.RWMutex
	closed  bool
	skip    atomic.Bool
	done    sync.WaitGroup
}

func newFanout(workers, queue int, h Handler, log *slog.Logger) *fanout {
	f := &fanout{queues: make([]chan job, workers), handler: h, log: log, stop: make(chan struct{})}
	for i := range f.queues {
		f.queues[i] = make(chan job, queue)
		f.done.Go(func() { f.work(f.queues[i]) })
	}
	return f
}

func (f *fanout) work(queue chan job) {
	for j := range queue {
		if j.mark != nil {
			j.mark.pass()
			continue
		}
		if !f.skip.Load() {
			f.deliver(j)
		}
	}
}

func (f *fanout) deliver(j job) {
	defer func() {
		if v := recover(); v != nil {
			f.panics.Add(1)
			f.log.Error("fanout handler panicked; skipped the event", "tenant", j.env.Tenant, "room", j.env.Room, "kind", j.env.Kind, "seq", j.env.Seq, "panic", fmt.Sprint(v))
		}
	}()
	if j.publish {
		f.handler.Publish(j.env)
		return
	}
	f.handler.Event(j.env)
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

func (f *fanout) barrier(ctx context.Context) error {
	m := &barrierMark{passed: make(chan struct{})}
	m.left.Store(int64(len(f.queues)))
	f.mu.RLock()
	if f.closed {
		f.mu.RUnlock()
		return errFanoutStopped
	}
	for _, q := range f.queues {
		select {
		case q <- job{mark: m}:
		case <-f.stop:
			f.mu.RUnlock()
			return errFanoutStopped
		case <-ctx.Done():
			f.mu.RUnlock()
			return ctx.Err()
		}
	}
	f.mu.RUnlock()
	select {
	case <-m.passed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
