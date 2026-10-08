package dedupe

import (
	"context"
	"testing"
	"time"
)

type signalingStore struct {
	*Store
	committed chan struct{}
}

func (s signalingStore) Commit(ctx context.Context, entries []Entry) error {
	err := s.Store.Commit(ctx, entries)
	s.committed <- struct{}{}
	return err
}

func TestBatchedReserveMatchesTheStoreOnRedis(t *testing.T) {
	mr, rdb := newRedis(t)
	other := newStore(t, rdb, "core-b", nil)
	reg := signalingStore{Store: newStore(t, rdb, "core-a", nil), committed: make(chan struct{}, 1)}
	b, _ := startBatcher(t, reg, BatchConfig{})
	x, y, z := key("x"), key("y"), key("z")

	expectStatuses(t, batchReserve(t, b, x), Reserved)
	expectValue(t, mr, x, "p:core-a")
	expectStatuses(t, batchReserve(t, b, x), PendingHere)
	expectNil(t, b.Commit(t.Context(), []Entry{{Key: x, Record: sampleRecord}}))
	select {
	case <-reg.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("batched commit never reached redis")
	}
	got := batchReserve(t, b, x, y)
	expectStatuses(t, got, Committed, Reserved)
	if !sameRecord(got[0].Record, sampleRecord) {
		t.Fatalf("committed record = %+v, want %+v", got[0].Record, sampleRecord)
	}
	expectStatuses(t, reserve(t, other, z), Reserved)
	expectStatuses(t, batchReserve(t, b, z), PendingElsewhere)
}

func batchReserve(t *testing.T, b *Batcher, keys ...Key) []Verdict {
	t.Helper()
	got, err := b.Reserve(t.Context(), keys)
	if err != nil {
		t.Fatalf("batched Reserve(%v): %v", keys, err)
	}
	if len(got) != len(keys) {
		t.Fatalf("batched Reserve answered %d of %d keys", len(got), len(keys))
	}
	return got
}
