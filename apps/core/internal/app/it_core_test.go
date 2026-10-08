package app

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

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const itLiveLimit = 30 * time.Second

var itFastEffects = map[string]string{"CORE_DRAIN_DELAY": "200ms", "RECONCILE_DELAY": "2s", "PUB_ACK_TIMEOUT": "500ms"}

type itCore struct {
	cfg     config.Config
	started <-chan struct{}
}

func startCore(t *testing.T, it *itInfra, env map[string]string) itCore {
	t.Helper()
	cfg := it.coreConfig(t, env)
	started := make(chan struct{})
	logger := slog.New(&termStartSignal{
		Handler: slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}),
		once:    &sync.Once{},
		started: started,
	})
	ctx, cancel := context.WithCancel(context.Background())
	core := &running{done: make(chan struct{})}
	go func() {
		defer close(core.done)
		core.err = run(ctx, cfg, logger)
	}()
	t.Cleanup(func() {
		cancel()
		<-core.done
	})
	awaitReady(t, cfg, core)
	return itCore{cfg: cfg, started: started}
}

func (c itCore) awaitTerm(t *testing.T) {
	t.Helper()
	select {
	case <-c.started:
	case <-time.After(itLiveLimit):
		t.Fatalf("no reconcile term started within %v", itLiveLimit)
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

func assertNoLiveIDs(t *testing.T, live <-chan *nats.Msg, wait time.Duration, unwantedIDs ...string) {
	t.Helper()
	unwanted := make(map[string]bool, len(unwantedIDs))
	for _, id := range unwantedIDs {
		unwanted[id] = true
	}
	timeout := time.After(wait)
	for {
		select {
		case m := <-live:
			if id := m.Header.Get(jetstream.MsgIDHeader); unwanted[id] {
				t.Fatalf("live event %s arrived, want none within %v", id, wait)
			}
		case <-timeout:
			return
		}
	}
}

type termStartSignal struct {
	slog.Handler
	once    *sync.Once
	started chan struct{}
}

func (h *termStartSignal) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *termStartSignal) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "reconcile term started" {
		h.once.Do(func() { close(h.started) })
	}
	if !h.Handler.Enabled(ctx, r.Level) {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

func (h *termStartSignal) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &termStartSignal{Handler: h.Handler.WithAttrs(attrs), once: h.once, started: h.started}
}

func (h *termStartSignal) WithGroup(name string) slog.Handler {
	return &termStartSignal{Handler: h.Handler.WithGroup(name), once: h.once, started: h.started}
}
