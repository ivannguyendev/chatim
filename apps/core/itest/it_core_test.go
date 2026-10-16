package itest

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/app"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const itLiveLimit = 30 * time.Second

const itProbeTimeout = 2 * time.Second

const itSweeperStarted = "event sweeper started"

var itFastSweep = map[string]string{"CORE_DRAIN_DELAY": "200ms", "PUB_ACK_TIMEOUT": "500ms", "SWEEP_MIN_AGE": "2s", "SWEEP_INTERVAL": "200ms", "STATE_CLEAR_TIMEOUT": "1s"}

type itCore struct {
	cfg     config.Config
	started <-chan struct{}
}

func startCore(t *testing.T, it *itInfra, env map[string]string) itCore {
	t.Helper()
	cfg := it.coreConfig(t, env)
	started := make(chan struct{})
	logger := slog.New(&sweeperStartSignal{
		Handler: slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}),
		once:    &sync.Once{},
		started: started,
	})
	ctx, cancel := context.WithCancel(context.Background())
	core := &running{done: make(chan struct{})}
	go func() {
		defer close(core.done)
		core.err = app.Run(ctx, cfg, logger)
	}()
	t.Cleanup(func() {
		cancel()
		<-core.done
	})
	awaitReady(t, cfg, core)
	return itCore{cfg: cfg, started: started}
}

func (c itCore) awaitSweeper(t *testing.T) {
	t.Helper()
	select {
	case <-c.started:
	case <-time.After(itLiveLimit):
		t.Fatalf("no event sweeper started within %v", itLiveLimit)
	}
}

func parseRoom(t *testing.T, roomID string) uint64 {
	t.Helper()
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		t.Fatalf("ParseRoomID(%s): %v", roomID, err)
	}
	return room
}

func subscribeLive(t *testing.T, it *itInfra, cfg config.Config, roomID string) <-chan *nats.Msg {
	t.Helper()
	live := make(chan *nats.Msg, 256)
	sub, err := it.nc.ChanSubscribe(cfg.Stream.LiveRoot+"."+itTenant+".*."+roomID+".>", live)
	if err != nil {
		t.Fatalf("subscribe live: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return live
}

func awaitLiveIDs(t *testing.T, live <-chan *nats.Msg, since time.Time, want ...string) {
	t.Helper()
	missing := make(map[string]bool, len(want))
	for _, id := range want {
		missing[id] = true
	}
	deadline := time.After(itLiveLimit)
	for len(missing) > 0 {
		select {
		case m := <-live:
			delete(missing, m.Header.Get(jetstream.MsgIDHeader))
		case <-deadline:
			t.Fatalf("live events %v did not arrive within %v", slices.Sorted(maps.Keys(missing)), itLiveLimit)
		}
	}
	t.Logf("live events %v arrived %v after the write", want, time.Since(since))
}

type sweeperStartSignal struct {
	slog.Handler
	once    *sync.Once
	started chan struct{}
}

func (h *sweeperStartSignal) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *sweeperStartSignal) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == itSweeperStarted {
		h.once.Do(func() { close(h.started) })
	}
	if !h.Handler.Enabled(ctx, r.Level) {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

func (h *sweeperStartSignal) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sweeperStartSignal{Handler: h.Handler.WithAttrs(attrs), once: h.once, started: h.started}
}

func (h *sweeperStartSignal) WithGroup(name string) slog.Handler {
	return &sweeperStartSignal{Handler: h.Handler.WithGroup(name), once: h.once, started: h.started}
}
