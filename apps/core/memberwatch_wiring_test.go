package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type fakeWatch struct {
	startErr error
	started  atomic.Bool
	stopped  atomic.Bool
}

func (w *fakeWatch) Start(context.Context) error {
	w.started.Store(true)
	return w.startErr
}

func (w *fakeWatch) Stop() { w.stopped.Store(true) }

func TestServeNeverOpensGRPCWhenTheMemberWatchFailsToStart(t *testing.T) {
	running := make(chan struct{})
	close(running)
	watch := &fakeWatch{startErr: errBroken}
	rg := startGate(t, t.Context(), &gatedRouter{running: running}, idle{}, watch)
	if err := rg.wait(t); !errors.Is(err, errBroken) {
		t.Fatalf("serve = %v, want the member watch error", err)
	}
	rg.assertGRPCNeverServed(t)
	if !watch.stopped.Load() {
		t.Error("member watch not stopped after its failed start")
	}
}

func TestServeStartsTheMemberWatchOnlyAfterTheRouterRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	watch := &fakeWatch{}
	rg := startGate(t, ctx, &gatedRouter{running: make(chan struct{})}, idle{}, watch)
	rg.awaitAdmin(t)
	cancel()
	if err := rg.wait(t); err != nil {
		t.Fatalf("serve = %v, want a clean stop", err)
	}
	if watch.started.Load() {
		t.Error("member watch started before the router ran")
	}
	if !watch.stopped.Load() {
		t.Error("member watch not stopped on shutdown")
	}
}
