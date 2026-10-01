package flush_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var validConfig = flush.Config{Shards: 2, Window: time.Millisecond, MaxBatch: 8, QueueSize: 4}

func TestNewValidatesConfig(t *testing.T) {
	tests := map[string]func(*flush.Config){
		"zero shards":       func(c *flush.Config) { c.Shards = 0 },
		"negative shards":   func(c *flush.Config) { c.Shards = -1 },
		"zero window":       func(c *flush.Config) { c.Window = 0 },
		"negative window":   func(c *flush.Config) { c.Window = -time.Millisecond },
		"zero max batch":    func(c *flush.Config) { c.MaxBatch = 0 },
		"zero queue size":   func(c *flush.Config) { c.QueueSize = 0 },
		"negative queue sz": func(c *flush.Config) { c.QueueSize = -4 },
	}
	for name, mutate := range tests {
		cfg := validConfig
		mutate(&cfg)
		if _, err := flush.New(&fakeStore{}, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := flush.New(nil, validConfig); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("New(nil store) = %v, want ErrInvalidArgument", err)
	}
	if _, err := flush.New(&fakeStore{}, validConfig); err != nil {
		t.Errorf("New(valid) = %v", err)
	}
}

func TestSubmitRejectsInvalidGroups(t *testing.T) {
	f, err := flush.New(&fakeStore{}, validConfig)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]flush.Group{
		"no messages": {Room: 1, Done: func([]store.Result) {}},
		"no done":     {Room: 1, Msgs: []domain.Message{{Room: 1, Seq: 1}}},
	}
	for name, g := range tests {
		if err := f.Submit(t.Context(), g); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Submit = %v, want ErrInvalidArgument", name, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.Submit(ctx, newRecorder().group("a", 1, 1)); !errors.Is(err, context.Canceled) {
		t.Errorf("Submit(cancelled ctx) = %v, want context.Canceled", err)
	}
}

func TestSubmitReturnsBusyWhenShardQueueIsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		fs, rec := &fakeStore{gate: gate}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: time.Millisecond, MaxBatch: 1, QueueSize: 2})
		h.submit(t, rec.group("a", 1, 1))
		synctest.Wait()
		h.submit(t, rec.group("b", 1, 2))
		h.submit(t, rec.group("c", 1, 3))

		begin := time.Now()
		if err := h.Submit(t.Context(), rec.group("d", 1, 4)); !errors.Is(err, domain.ErrBusy) {
			t.Fatalf("Submit on full queue = %v, want ErrBusy", err)
		}
		if waited := time.Since(begin); waited != 0 {
			t.Errorf("Submit blocked %v on a full queue", waited)
		}
		close(gate)
		h.stop(t)
		assertSizes(t, fs, 1, 1, 1)
		rec.assertCalledOnce(t, "a", "b", "c")
	})
}

func TestSubmitAfterCloseAsksToRetryLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		h := start(t, &fakeStore{}, validConfig)
		h.stop(t)
		if err := h.Submit(t.Context(), rec.group("a", 1, 1)); !errors.Is(err, domain.ErrRetryLater) {
			t.Fatalf("Submit after Close = %v, want ErrRetryLater", err)
		}
		if err := h.Close(t.Context()); err != nil {
			t.Errorf("second Close = %v, want nil", err)
		}
		rec.assertCalledOnce(t)
	})
}

func TestRunTwiceFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := start(t, &fakeStore{}, validConfig)
		synctest.Wait()
		if err := h.Run(t.Context()); err == nil {
			t.Fatal("second Run = nil, want an error")
		}
		h.stop(t)
	})
}
