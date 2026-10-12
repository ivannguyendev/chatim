package flush_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var deadlineConfig = flush.Config{Shards: 1, Window: 10 * time.Millisecond, MaxBatch: 64, QueueSize: 8, InsertTimeout: 50 * time.Millisecond}

type deadlineSpy struct {
	*fakeStore
	mu        sync.Mutex
	deadlines []time.Time
}

func (s *deadlineSpy) Insert(ctx context.Context, msgs []domain.Message) []store.Result {
	dl, _ := ctx.Deadline()
	s.mu.Lock()
	s.deadlines = append(s.deadlines, dl)
	s.mu.Unlock()
	return s.fakeStore.Insert(ctx, msgs)
}

func TestGroupThatCannotFinishByItsDeadlineIsNotSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		spy, rec := &deadlineSpy{fakeStore: &fakeStore{}}, newRecorder()
		h := start(t, spy, deadlineConfig)
		begin := time.Now()
		late, edge, plain := rec.group("late", 1, 1, 2), rec.group("edge", 2, 1), rec.group("plain", 3, 1)
		late.Deadline = begin.Add(59 * time.Millisecond)
		edge.Deadline = begin.Add(60 * time.Millisecond)
		for _, g := range []flush.Group{late, edge, plain} {
			h.submit(t, g)
		}
		time.Sleep(deadlineConfig.Window)
		synctest.Wait()

		assertSizes(t, spy.fakeStore, 2)
		for _, r := range assertOutcome(t, rec, "late", 2, store.Unknown) {
			if !errors.Is(r.Err, flush.ErrNotSent) || !errors.Is(r.Err, domain.ErrRetryLater) {
				t.Fatalf("late group err = %v, want ErrNotSent wrapping ErrRetryLater", r.Err)
			}
		}
		assertOutcome(t, rec, "edge", 1, store.Inserted)
		assertOutcome(t, rec, "plain", 1, store.Inserted)
		if want := []time.Time{edge.Deadline}; !slices.EqualFunc(spy.deadlines, want, time.Time.Equal) {
			t.Fatalf("insert deadlines = %v, want %v so the sent insert ends by its group deadline", spy.deadlines, want)
		}
		h.stop(t)
		rec.assertCalledOnce(t, "late", "edge", "plain")
	})
}

func TestBatchOfExpiredGroupsNeverReachesTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fs, rec := &fakeStore{}, newRecorder()
		h := start(t, fs, deadlineConfig)
		for i, id := range []string{"a", "b"} {
			g := rec.group(id, uint64(i+1), 1)
			g.Deadline = time.Now()
			h.submit(t, g)
		}
		h.stop(t)
		assertSizes(t, fs)
		for _, id := range []string{"a", "b"} {
			if r := assertOutcome(t, rec, id, 1, store.Unknown); !errors.Is(r[0].Err, flush.ErrNotSent) {
				t.Fatalf("group %s err = %v, want ErrNotSent", id, r[0].Err)
			}
		}
	})
}
