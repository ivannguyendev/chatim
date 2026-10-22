package app

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunIsReadyOnlyAfterNATSAndStaysReadyWhenNATSDrops(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	_, dial := startFakeCore(t)
	nats := heldNATS()
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, testConfig(mr.Addr(), keys.path), logs.logger(), Deps{Dial: dial, Listen: lis.listen, NATS: nats.dial})

	adminAddr := awaitListener(t, lis.admin)
	receive(t, nats.dialed, "nats dial")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 503 {
		t.Fatalf("/readyz = %d before NATS, want 503", code)
	}
	if _, body := httpStatus(t, adminAddr, "/metrics"); !strings.Contains(body, "chatim_websocket_nats_connected 0") {
		t.Fatal("/metrics lacks nats_connected 0 before NATS")
	}
	select {
	case <-lis.ws:
		t.Fatal("websocket listener opened before NATS")
	default:
	}

	close(nats.release)
	awaitListener(t, lis.ws)
	logs.await(t, "websocket ready")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 200 {
		t.Fatalf("/readyz = %d after start, want 200", code)
	}
	if nats.running() != 2 || len(nats.streams) != 1 || nats.streams[0] != "CHATIM_WS" {
		t.Fatalf("running consumers %d, ensured streams %v; want two consumers and CHATIM_WS", nats.running(), nats.streams)
	}
	_, body := httpStatus(t, adminAddr, "/metrics")
	for _, want := range []string{
		"chatim_websocket_nats_connected 1",
		`chatim_websocket_consumer_resets_total{stream="CHATIM_EVT"} 0`,
		`chatim_websocket_consumer_pending{stream="CHATIM_WS"} 0`,
		"chatim_websocket_fanout_queue_depth 0",
		"chatim_websocket_events_unknown_total 0",
		"chatim_websocket_nats_reconnects_total 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}

	nats.connected.Store(false)
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 200 {
		t.Fatalf("/readyz = %d after NATS dropped, want still 200", code)
	}
	if _, body := httpStatus(t, adminAddr, "/metrics"); !strings.Contains(body, "chatim_websocket_nats_connected 0") {
		t.Fatal("nats_connected did not drop to 0")
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if nats.running() != 0 || nats.closed.Load() != 1 {
		t.Fatalf("after stop: running consumers %d, NATS closes %d; want 0 and 1", nats.running(), nats.closed.Load())
	}
}

func TestRunFailsWhenNATSCannotBeOpened(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	nats := newFakeNATS()
	nats.dialErr = errors.New("nats not connected within 10s")
	lis := newListeners()
	err := Run(t.Context(), testConfig(mr.Addr(), keys.path), newLogWatch().logger(), Deps{Listen: lis.listen, NATS: nats.dial})
	if err == nil || !strings.Contains(err.Error(), "open nats") {
		t.Fatalf("Run = %v, want an open nats error", err)
	}
	if len(lis.admin) != 1 || len(lis.ws) != 0 {
		t.Fatalf("listeners opened: admin %d, websocket %d; want only admin", len(lis.admin), len(lis.ws))
	}
}

func TestRunStopsCleanlyWhenSignalledWhileWaitingForNATS(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	nats := heldNATS()
	lis := newListeners()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, testConfig(mr.Addr(), keys.path), newLogWatch().logger(), Deps{Listen: lis.listen, NATS: nats.dial})
	receive(t, nats.dialed, "nats dial")
	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run = %v, want a clean stop", err)
	}
	if len(lis.ws) != 0 {
		t.Fatal("websocket listener opened after the stop signal")
	}
}
