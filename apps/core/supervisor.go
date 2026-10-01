package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

var errExited = errors.New("returned without being stopped")

type supervisor struct {
	base     context.Context
	failed   chan error
	stopping atomic.Bool
}

type task struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func newSupervisor(parent context.Context, capacity int) *supervisor {
	return &supervisor{base: context.WithoutCancel(parent), failed: make(chan error, capacity)}
}

func (s *supervisor) start(name string, run func(context.Context) error) *task {
	ctx, cancel := context.WithCancel(s.base)
	t := &task{name: name, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(t.done)
		t.err = run(ctx)
		if s.stopping.Load() {
			return
		}
		cause := t.err
		if cause == nil {
			cause = errExited
		}
		select {
		case s.failed <- fmt.Errorf("%s stopped unexpectedly: %w", name, cause):
		default:
		}
	}()
	return t
}

func (s *supervisor) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-s.failed:
		return err
	}
}

func (t *task) stop(ctx context.Context) error {
	t.cancel()
	select {
	case <-t.done:
	case <-ctx.Done():
		return fmt.Errorf("%s did not stop in time: %w", t.name, ctx.Err())
	}
	if t.err != nil && !errors.Is(t.err, context.Canceled) {
		return fmt.Errorf("%s: %w", t.name, t.err)
	}
	return nil
}
