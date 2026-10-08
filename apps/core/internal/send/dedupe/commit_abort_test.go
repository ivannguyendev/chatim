package dedupe

import (
	"testing"
)

func TestCommitOverwritesUnconditionallyWithCommittedTTL(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b := newStore(t, rdb, "core-a", nil), newStore(t, rdb, "core-b", nil)
	x, y := key("x"), key("y")
	expectStatuses(t, reserve(t, b, x), Reserved)
	second := Record{Seq: 8, CreatedAt: sampleRecord.CreatedAt}
	if err := a.Commit(t.Context(), []Entry{{Key: x, Record: sampleRecord}, {Key: y, Record: second}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	expectValue(t, mr, x, "c:7:1700000000123")
	expectValue(t, mr, y, "c:8:1700000000123")
	for _, k := range []Key{x, y} {
		if ttl := mr.TTL(k.String()); ttl != DefaultCommittedTTL {
			t.Fatalf("committed ttl of %s = %v, want %v", k, ttl, DefaultCommittedTTL)
		}
	}
	mr.FastForward(DefaultCommittedTTL)
	if mr.Exists(x.String()) || mr.Exists(y.String()) {
		t.Fatal("committed records outlived the committed ttl")
	}
	expectStatuses(t, reserve(t, b, x), Reserved)
}

func TestAbortDeletesOnlyThisCoresPendingReservations(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b := newStore(t, rdb, "core-a", nil), newStore(t, rdb, "core-b", nil)
	mine, theirs, done, missing, hash := key("mine"), key("theirs"), key("done"), key("missing"), key("hash")
	expectStatuses(t, reserve(t, a, mine, done), Reserved, Reserved)
	expectStatuses(t, reserve(t, b, theirs), Reserved)
	if err := a.Commit(t.Context(), []Entry{{Key: done, Record: sampleRecord}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	mr.HSet(hash.String(), "f", "v")

	if err := a.Abort(t.Context(), []Key{mine, theirs, done, missing, hash}); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if mr.Exists(mine.String()) || mr.Exists(missing.String()) {
		t.Fatal("own pending reservation survived Abort")
	}
	expectValue(t, mr, theirs, "p:core-b")
	expectValue(t, mr, done, committedValue(sampleRecord))
	if !mr.Exists(hash.String()) {
		t.Fatal("Abort deleted a key of another type")
	}
	expectStatuses(t, reserve(t, b, mine), Reserved)
}
