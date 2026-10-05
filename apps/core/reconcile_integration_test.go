package main

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func TestRealInfraReaderForwardsWritesThatSkippedTheCore(t *testing.T) {
	it := realInfra(t)
	cfg := it.coreConfig(t, map[string]string{"CORE_DRAIN_DELAY": "200ms", "RECONCILE_DELAY": "2s", "PUB_ACK_TIMEOUT": "500ms"})
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
	select {
	case <-termStarted:
	case <-time.After(30 * time.Second):
		t.Fatalf("reader did not start a term within 30s")
	}

	roomID := createRoom(t, dialCore(t, cfg))
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		t.Fatalf("ParseRoomID(%s): %v", roomID, err)
	}

	inserted := time.Now()
	outside := domain.Message{Room: room, Seq: 1, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "written outside the core", CID: "outside-1", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	st := mongostore.New(it.mongo.Database(cfg.MongoDB), mongostore.Options{})
	if res := st.Insert(t.Context(), []domain.Message{outside}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert outside the core: %+v", res)
	}

	want := []string{
		work.Record{Kind: store.RoomInserted, Room: room}.ID(),
		work.Record{Kind: store.MessageInserted, Room: room, Seq: 1}.ID(),
	}
	awaitRecords(t, it, cfg.Work.Name, want)
	t.Logf("records %v reached the work stream %v after the insert", want, time.Since(inserted))
}

func awaitRecords(t *testing.T, it *itInfra, stream string, want []string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	seen := map[string]bool{}
	for {
		got := it.streamIDs(t, stream)
		for id, n := range got {
			if n > 0 {
				seen[id] = true
			}
		}
		missing := slices.DeleteFunc(slices.Clone(want), func(id string) bool { return seen[id] })
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("work stream %s lacks %v 30s after the insert; has %v", stream, missing, got)
		}
		<-poll.C
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
