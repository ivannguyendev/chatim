package app

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
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
	name  string
	order *stopOrder
}

func (recordedWatch) Start(context.Context) error { return nil }

func (w recordedWatch) Stop() {
	w.order.mu.Lock()
	defer w.order.mu.Unlock()
	w.order.got = append(w.order.got, w.name)
}

type hungDrainer struct {
	cancelled chan struct{}
}

func (h hungDrainer) Run(ctx context.Context) error {
	<-ctx.Done()
	close(h.cancelled)
	return ctx.Err()
}

func (hungDrainer) Close(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func stopNode(order *stopOrder) *node {
	running := make(chan struct{})
	close(running)
	cfg := config.Config{
		DrainDelay: 10 * time.Millisecond, GRPCShutdown: time.Second, RequestDeadline: 500 * time.Millisecond,
		PublisherDrain: time.Second, ShutdownBudget: gateLimit, Flush: flush.Config{InsertTimeout: 100 * time.Millisecond},
		Projection: projection.Config{FetchWait: 10 * time.Millisecond}, Mention: mentionapp.Config{Enabled: true, FetchWait: 10 * time.Millisecond},
	}
	return &node{
		cfg: cfg, log: quiet,
		admin:     admin.New(admin.Config{ShutdownTimeout: config.CloseTimeout}, quiet),
		grpc:      grpcserver.New(grpcserver.Config{ShutdownTimeout: time.Second}, quiet),
		publisher: order.drainer("publisher"), stateClears: order.drainer("state clear queue"),
		flusher: order.drainer("flusher"), cidBatch: order.drainer("cid batcher"),
		router: &recordedRouter{recordedDrainer: order.drainer("router"), running: running},
		slots:  idle{}, timers: order.drainer("timer consumer"),
		projections: order.drainer("projection"), mention: order.drainer("mention"), sweeper: order.drainer("sweeper"),
		watches: liveWatchers{recordedWatch{name: "member watch", order: order}, recordedWatch{name: "room watch", order: order}},
	}
}

func serveUntilStopped(t *testing.T, a *node) error {
	t.Helper()
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
		return err
	case <-time.After(gateLimit):
		t.Fatalf("serve did not return within %v", gateLimit)
		return nil
	}
}

func TestShutdownStopsTheSweeperFirstAndClearsStatesAfterThePublisher(t *testing.T) {
	order := &stopOrder{}
	if err := serveUntilStopped(t, stopNode(order)); err != nil {
		t.Fatalf("serve = %v, want a clean stop", err)
	}
	want := []string{
		"sweeper", "mention", "projection", "timer consumer", "member watch", "room watch", "router", "cid batcher", "flusher", "publisher", "state clear queue",
	}
	got := order.names()
	if len(got) == len(want) {
		slices.Sort(got[1:3])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("drain order = %v, want %v", got, want)
	}
}

func TestShutdownCancelsAComponentWhoseDrainTimesOut(t *testing.T) {
	order := &stopOrder{}
	a := stopNode(order)
	hung := hungDrainer{cancelled: make(chan struct{})}
	a.sweeper = hung
	err := serveUntilStopped(t, a)
	if err == nil || !strings.Contains(err.Error(), "sweeper") {
		t.Fatalf("serve = %v, want the sweeper step reported as failed", err)
	}
	select {
	case <-hung.cancelled:
	default:
		t.Fatal("the sweeper kept running after its drain timed out")
	}
	if got := order.names(); !slices.Contains(got, "state clear queue") {
		t.Fatalf("drain order = %v, want the rest of the stop to go on", got)
	}
}
