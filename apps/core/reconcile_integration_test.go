package main

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func TestRealInfraReconcilerPublishesWritesThatSkippedTheCore(t *testing.T) {
	it := realInfra(t)
	cfg := it.coreConfig(t, map[string]string{"CORE_DRAIN_DELAY": "200ms", "RECONCILE_DELAY": "1s"})
	termStarted := make(chan struct{})
	logger := slog.New(&termStartSignal{
		Handler: slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}),
		once:    &sync.Once{},
		started: termStarted,
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

	roomID := createRoom(t, dialCore(t, cfg))
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		t.Fatalf("ParseRoomID(%s): %v", roomID, err)
	}
	live := make(chan *nats.Msg, 16)
	sub, err := it.nc.ChanSubscribe(cfg.Stream.LiveRoot+"."+itTenant+".room."+roomID+".>", live)
	if err != nil {
		t.Fatalf("subscribe live: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	select {
	case <-termStarted:
	case <-time.After(30 * time.Second):
		t.Fatalf("reconciler did not start a term within 30s")
	}

	inserted := time.Now()
	outside := domain.Message{Room: room, Seq: 1, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "written outside the core", CID: "outside-1", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	st := mongostore.New(it.mongo.Database(cfg.MongoDB), mongostore.Options{})
	if res := st.Insert(t.Context(), []domain.Message{outside}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert outside the core: %+v", res)
	}

	want := pbconv.MessageEventID(room, 0, 1)
	deadline := time.After(30 * time.Second)
	for {
		select {
		case m := <-live:
			if m.Header.Get(jetstream.MsgIDHeader) == want {
				t.Logf("live event %s arrived %v after the insert", want, time.Since(inserted))
				return
			}
		case <-deadline:
			t.Fatalf("no live event %s within 30s of a write that skipped the core", want)
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
