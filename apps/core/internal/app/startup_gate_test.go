package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

const (
	grpcListening = "grpc server listening"
	coreReady     = "core ready"
	gateLimit     = 10 * time.Second
)

type idle struct {
	run func(ctx context.Context) error
}

func (c idle) Run(ctx context.Context) error {
	if c.run != nil {
		return c.run(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (idle) Close(context.Context) error { return nil }

type gatedRouter struct {
	idle
	running chan struct{}
}

func (r *gatedRouter) Running() <-chan struct{} { return r.running }

type gateRig struct {
	node      *node
	sink      *testlog.Sink
	adminAddr string
	grpcAddr  string
	done      chan error
}

func startGate(t *testing.T, ctx context.Context, router *gatedRouter, slots runner, watch *fakeWatch) *gateRig {
	t.Helper()
	sink := &testlog.Sink{}
	log := sink.Logger()
	cfg := config.Config{
		DrainDelay: 10 * time.Millisecond, GRPCShutdown: time.Second, RequestDeadline: 500 * time.Millisecond,
		PublisherDrain: time.Second, ShutdownBudget: gateLimit, Flush: flush.Config{InsertTimeout: 100 * time.Millisecond},
	}
	a := &node{
		cfg: cfg, log: log,
		admin:     admin.New(admin.Config{ShutdownTimeout: config.CloseTimeout}, log),
		grpc:      grpcserver.New(grpcserver.Config{ShutdownTimeout: time.Second}, log),
		publisher: idle{}, flusher: idle{}, cidBatch: idle{}, router: router, slots: slots, workers: idle{},
		memberWatch: watch,
	}
	lis, err := listenAll(t.Context(), "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	rg := &gateRig{node: a, sink: sink, adminAddr: lis[0].Addr().String(), grpcAddr: lis[1].Addr().String(), done: make(chan error, 1)}
	go func() { rg.done <- a.serve(ctx, lis[0], lis[1]) }()
	return rg
}

func (rg *gateRig) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-rg.done:
		return err
	case <-time.After(gateLimit):
		t.Fatalf("serve did not return within %v", gateLimit)
		return nil
	}
}

func (rg *gateRig) awaitAdmin(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(gateLimit)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+rg.adminAddr+"/livez", http.NoBody)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("admin port never answered: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (rg *gateRig) assertGRPCNeverServed(t *testing.T) {
	t.Helper()
	if n := rg.sink.Count(grpcListening); n != 0 {
		t.Errorf("grpc served %d times, want never", n)
	}
	if n := rg.sink.Count(coreReady); n != 0 {
		t.Errorf("core reported ready %d times, want never", n)
	}
	if conn, err := net.Dial("tcp", rg.grpcAddr); err == nil {
		_ = conn.Close()
		t.Error("grpc port still accepts connections, want its listener closed")
	}
}

func TestServeNeverOpensGRPCWhenStopRacesStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	rg := startGate(t, ctx, &gatedRouter{running: make(chan struct{})}, idle{}, &fakeWatch{})
	rg.awaitAdmin(t)
	if err := Probe(t.Context(), rg.adminAddr); err == nil {
		t.Fatal("readyz answered 200 before the router ran")
	}
	cancel()
	if err := rg.wait(t); err != nil {
		t.Fatalf("serve = %v, want a clean stop", err)
	}
	rg.assertGRPCNeverServed(t)
}

func TestServeNeverOpensGRPCWhenASiblingFailsAtStartup(t *testing.T) {
	failing := idle{run: func(context.Context) error { return errBroken }}
	rg := startGate(t, t.Context(), &gatedRouter{running: make(chan struct{})}, failing, &fakeWatch{})
	if err := rg.wait(t); !errors.Is(err, errBroken) {
		t.Fatalf("serve = %v, want the failing sibling's error", err)
	}
	rg.assertGRPCNeverServed(t)
}

func TestServeOpensGRPCAndReadinessAfterACleanStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	running := make(chan struct{})
	close(running)
	rg := startGate(t, ctx, &gatedRouter{running: running}, idle{}, &fakeWatch{})
	rg.awaitAdmin(t)
	deadline := time.Now().Add(gateLimit)
	for Probe(t.Context(), rg.adminAddr) != nil {
		if time.Now().After(deadline) {
			t.Fatal("readyz never answered 200 after a clean start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	conn, err := grpc.NewClient(rg.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()
	resp, err := healthpb.NewHealthClient(conn).Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v, %v, want SERVING", resp.GetStatus(), err)
	}
	cancel()
	if err := rg.wait(t); err != nil {
		t.Fatalf("serve = %v, want a clean stop", err)
	}
	if rg.sink.Count(grpcListening) != 1 || rg.sink.Count(coreReady) != 1 {
		t.Fatalf("grpc served %d times and ready %d times, want once each", rg.sink.Count(grpcListening), rg.sink.Count(coreReady))
	}
}
