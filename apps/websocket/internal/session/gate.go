package session

import (
	"context"
	"sync/atomic"
)

type gate struct {
	count  atomic.Int64
	closed atomic.Bool
	idle   chan struct{}
}

func newGate() *gate { return &gate{idle: make(chan struct{}, 1)} }

func (g *gate) enter() bool {
	g.count.Add(1)
	if g.closed.Load() {
		g.leave()
		return false
	}
	return true
}

func (g *gate) leave() {
	if g.count.Add(-1) == 0 && g.closed.Load() {
		select {
		case g.idle <- struct{}{}:
		default:
		}
	}
}

func (g *gate) close() { g.closed.Store(true) }

func (g *gate) wait(ctx context.Context) error {
	g.close()
	for g.count.Load() != 0 {
		select {
		case <-g.idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
