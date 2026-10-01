package flush_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type harness struct {
	*flush.Flusher
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

func start(t *testing.T, s store.Messages, cfg flush.Config) *harness {
	t.Helper()
	if cfg.InsertTimeout == 0 {
		cfg.InsertTimeout = time.Hour
	}
	f, err := flush.New(s, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{Flusher: f, cancel: cancel, done: make(chan error, 1)}
	go func() { h.done <- f.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = h.wait()
	})
	return h
}

func (h *harness) wait() error {
	h.once.Do(func() { h.err = <-h.done })
	return h.err
}

func (h *harness) submit(t *testing.T, g flush.Group) {
	t.Helper()
	if err := h.Submit(context.Background(), g); err != nil {
		t.Fatalf("Submit(room %d): %v", g.Room, err)
	}
}

func (h *harness) stop(t *testing.T) {
	t.Helper()
	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := h.wait(); err != nil {
		t.Fatalf("Run after Close = %v, want nil", err)
	}
}

func assertSizes(t *testing.T, s *fakeStore, want ...int) {
	t.Helper()
	var got []int
	for _, b := range s.all() {
		got = append(got, len(b.msgs))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("batch sizes = %v, want %v", got, want)
	}
}

func assertOutcome(t *testing.T, rec *recorder, id string, n int, o store.Outcome) []store.Result {
	t.Helper()
	got := rec.results(t, id)
	if len(got) != n {
		t.Fatalf("group %s: %d results, want %d", id, len(got), n)
	}
	for i, r := range got {
		if r.Outcome != o {
			t.Errorf("group %s result %d = %v (%v), want %v", id, i, r.Outcome, r.Err, o)
		}
	}
	return got
}
