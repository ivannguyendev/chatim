package flush_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestInsertTimeoutBoundsEachBatchAndShardKeepsServing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		fs, rec := &fakeStore{gate: gate}, newRecorder()
		h := start(t, fs, flush.Config{
			Shards: 1, Window: time.Millisecond, MaxBatch: 8, QueueSize: 8, InsertTimeout: 50 * time.Millisecond,
		})
		h.submit(t, rec.group("slow", 1, 1, 2))
		time.Sleep(50 * time.Millisecond)
		rec.assertCalledOnce(t)

		time.Sleep(2 * time.Millisecond)
		for _, r := range assertOutcome(t, rec, "slow", 2, store.Unknown) {
			if !errors.Is(r.Err, context.DeadlineExceeded) {
				t.Errorf("slow insert err = %v, want context.DeadlineExceeded", r.Err)
			}
		}

		close(gate)
		h.submit(t, rec.group("next", 1, 3))
		h.stop(t)
		assertSizes(t, fs, 2, 1)
		assertOutcome(t, rec, "next", 1, store.Inserted)
		rec.assertCalledOnce(t, "slow", "next")
	})
}
