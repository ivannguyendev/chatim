package flush_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestOneInsertInFlightPerShard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		fs, rec := &fakeStore{shards: 2, gate: gate}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 2, Window: time.Millisecond, MaxBatch: 256, QueueSize: 16})
		a, b := roomsIn(0, 2, 1)[0], roomsIn(1, 2, 1)[0]
		h.submit(t, rec.group("a1", a, 1))
		h.submit(t, rec.group("b1", b, 1))
		time.Sleep(2 * time.Millisecond)
		if n := fs.inFlight(); n != 2 {
			t.Fatalf("%d inserts in flight, want one per shard", n)
		}

		h.submit(t, rec.group("a2", a, 2))
		h.submit(t, rec.group("a3", a, 3))
		time.Sleep(5 * time.Millisecond)
		if n := len(fs.all()); n != 2 {
			t.Fatalf("%d inserts started, want 2 while both shards are busy", n)
		}
		close(gate)
		h.stop(t)

		all := fs.all()
		if len(all) != 3 || len(all[2].msgs) != 2 || all[2].msgs[0].Room != a {
			t.Fatalf("third insert = %+v, want a2 and a3 together", all[len(all)-1].msgs)
		}
		for shard, peak := range fs.peaks() {
			if peak != 1 {
				t.Errorf("shard %d had %d inserts in flight, want 1", shard, peak)
			}
		}
		rec.assertCalledOnce(t, "a1", "b1", "a2", "a3")
	})
}

func TestCloseDrainsQueuedGroupsWithoutWaitingForWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fs, rec := &fakeStore{shards: 3}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 3, Window: time.Hour, MaxBatch: 5, QueueSize: 64})
		begin := time.Now()
		var ids []string
		total := 0
		for i := range 30 {
			id := strconv.Itoa(i)
			seqs := make([]uint64, i%3+1)
			for j := range seqs {
				seqs[j] = uint64(i*3 + j + 1)
			}
			ids, total = append(ids, id), total+len(seqs)
			h.submit(t, rec.group(id, uint64(i%7+1), seqs...))
		}
		h.stop(t)
		if waited := time.Since(begin); waited != 0 {
			t.Errorf("drain waited %v for the window", waited)
		}
		rec.assertCalledOnce(t, ids...)
		for i, id := range ids {
			assertOutcome(t, rec, id, i%3+1, store.Inserted)
		}
		stored := 0
		for _, b := range fs.all() {
			stored += len(b.msgs)
		}
		if stored != total {
			t.Errorf("stored %d messages, want %d", stored, total)
		}
	})
}

func TestCloseRespectsItsContextAndKeepsInsertAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		fs, rec := &fakeStore{gate: gate}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: time.Millisecond, MaxBatch: 1, QueueSize: 8})
		h.submit(t, rec.group("a", 1, 1))
		h.submit(t, rec.group("b", 1, 2))
		time.Sleep(2 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if err := h.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close past its deadline = %v, want DeadlineExceeded", err)
		}
		if err := h.Submit(t.Context(), rec.group("c", 1, 3)); !errors.Is(err, domain.ErrRetryLater) {
			t.Errorf("Submit while draining = %v, want ErrRetryLater", err)
		}
		close(gate)
		if err := h.wait(); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
		rec.assertCalledOnce(t, "a", "b")
		assertOutcome(t, rec, "a", 1, store.Inserted)
		assertOutcome(t, rec, "b", 1, store.Inserted)
	})
}

func TestHardStopFailsInFlightCarriedAndQueuedGroups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		fs, rec := &fakeStore{gate: gate}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: time.Hour, MaxBatch: 2, QueueSize: 8})
		h.submit(t, rec.group("sent", 1, 1))
		synctest.Wait()
		h.submit(t, rec.group("carried", 1, 2, 3))
		synctest.Wait()
		h.submit(t, rec.group("queued", 1, 4))
		synctest.Wait()

		h.cancel()
		if err := h.wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel = %v, want context.Canceled", err)
		}
		assertSizes(t, fs, 1)
		assertOutcome(t, rec, "sent", 1, store.Unknown)
		for id, n := range map[string]int{"carried": 2, "queued": 1} {
			for _, r := range assertOutcome(t, rec, id, n, store.Unknown) {
				if !errors.Is(r.Err, context.Canceled) {
					t.Errorf("group %s err = %v, want context.Canceled", id, r.Err)
				}
			}
		}
		if err := h.Submit(t.Context(), rec.group("late", 1, 5)); !errors.Is(err, domain.ErrRetryLater) {
			t.Errorf("Submit after hard stop = %v, want ErrRetryLater", err)
		}
	})
}

func TestHardStopNeverSendsACollectedBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fs, rec := &fakeStore{}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: time.Hour, MaxBatch: 10, QueueSize: 8})
		h.submit(t, rec.group("p", 1, 1))
		h.submit(t, rec.group("q", 2, 1, 2))
		synctest.Wait()
		h.cancel()
		if err := h.wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel = %v, want context.Canceled", err)
		}
		assertSizes(t, fs)
		assertOutcome(t, rec, "p", 1, store.Unknown)
		assertOutcome(t, rec, "q", 2, store.Unknown)
	})
}

func TestResultCountMismatchMarksWholeBatchUnknown(t *testing.T) {
	responses := map[string]func([]domain.Message) []store.Result{
		"fewer": func(m []domain.Message) []store.Result { return uniform(len(m)-1, store.Inserted, nil) },
		"more":  func(m []domain.Message) []store.Result { return uniform(len(m)+1, store.Inserted, nil) },
		"none":  func([]domain.Message) []store.Result { return nil },
	}
	for name, respond := range responses {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fs, rec := &fakeStore{respond: respond}, newRecorder()
				h := start(t, fs, flush.Config{Shards: 1, Window: time.Millisecond, MaxBatch: 8, QueueSize: 8})
				h.submit(t, rec.group("a", 1, 1, 2))
				h.submit(t, rec.group("b", 2, 1))
				h.stop(t)
				assertSizes(t, fs, 3)
				for id, n := range map[string]int{"a": 2, "b": 1} {
					for _, r := range assertOutcome(t, rec, id, n, store.Unknown) {
						if r.Err == nil {
							t.Errorf("group %s: Unknown without an error", id)
						}
					}
				}
			})
		})
	}
}
