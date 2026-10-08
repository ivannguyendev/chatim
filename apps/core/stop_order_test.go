package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

type stopOrder struct {
	mu  sync.Mutex
	got []string
}

func (o *stopOrder) drainer(name string) recordedDrainer {
	return recordedDrainer{name: name, order: o}
}

func (o *stopOrder) names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.got)
}

type recordedDrainer struct {
	idle
	name  string
	order *stopOrder
}

func (d recordedDrainer) Close(context.Context) error {
	d.order.mu.Lock()
	defer d.order.mu.Unlock()
	d.order.got = append(d.order.got, d.name)
	return nil
}

type recordedRouter struct {
	recordedDrainer
	running chan struct{}
}

func (r *recordedRouter) Running() <-chan struct{} { return r.running }

type recordedWatch struct {
	order *stopOrder
}

func (recordedWatch) Start(context.Context) error { return nil }

func (w recordedWatch) Stop() {
	w.order.mu.Lock()
	defer w.order.mu.Unlock()
	w.order.got = append(w.order.got, "member watch")
}

func TestShutdownDrainsWorkersAfterTheReconcilerAndBeforeTheRouter(t *testing.T) {
	order := &stopOrder{}
	running := make(chan struct{})
	close(running)
	cfg := config.Config{
		DrainDelay: 10 * time.Millisecond, GRPCShutdown: time.Second, RequestDeadline: 500 * time.Millisecond,
		PublisherDrain: time.Second, ShutdownBudget: gateLimit, Flush: flush.Config{InsertTimeout: 100 * time.Millisecond},
	}
	a := &app{
		cfg: cfg, log: quiet,
		admin:     admin.New(admin.Config{ShutdownTimeout: config.CloseTimeout}, quiet),
		grpc:      grpcserver.New(grpcserver.Config{ShutdownTimeout: time.Second}, quiet),
		publisher: order.drainer("publisher"), flusher: order.drainer("flusher"), cidBatch: order.drainer("cid batcher"),
		router: &recordedRouter{recordedDrainer: order.drainer("router"), running: running},
		slots:  idle{}, workers: order.drainer("workers"), reconciler: order.drainer("reconciler"),
		memberWatch: recordedWatch{order: order},
	}
	lis, err := listenAll(t.Context(), "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.serve(ctx, lis[0], lis[1]) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve = %v, want a clean stop", err)
		}
	case <-time.After(gateLimit):
		t.Fatalf("serve did not return within %v", gateLimit)
	}
	want := []string{"reconciler", "workers", "member watch", "router", "cid batcher", "flusher", "publisher"}
	if got := order.names(); !slices.Equal(got, want) {
		t.Fatalf("drain order = %v, want %v", got, want)
	}
}
