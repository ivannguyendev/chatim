package dedupe

import (
	"fmt"
	"testing"
	"time"
)

func TestReserveReportsTheStateOfEachKey(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b := newStore(t, rdb, "core-a", nil), newStore(t, rdb, "core-b", nil)
	x, y := key("x"), key("y")

	expectStatuses(t, reserve(t, a, x, y), Reserved, Reserved)
	expectValue(t, mr, x, "p:core-a")
	if ttl := mr.TTL(x.String()); ttl != DefaultPendingTTL {
		t.Fatalf("pending ttl = %v, want %v", ttl, DefaultPendingTTL)
	}
	expectStatuses(t, reserve(t, a, x), PendingHere)
	expectStatuses(t, reserve(t, b, x, y), PendingElsewhere, PendingElsewhere)
	expectValue(t, mr, x, "p:core-a")

	if err := a.Commit(t.Context(), []Entry{{Key: x, Record: sampleRecord}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got := reserve(t, b, x, key("z"))
	expectStatuses(t, got, Committed, Reserved)
	if !sameRecord(got[0].Record, sampleRecord) {
		t.Fatalf("committed record = %+v, want %+v", got[0].Record, sampleRecord)
	}
	expectStatuses(t, reserve(t, a, x), Committed)
}

func TestPendingReservationExpiresAfterPendingTTL(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b := newStore(t, rdb, "core-a", nil), newStore(t, rdb, "core-b", nil)
	x := key("x")
	expectStatuses(t, reserve(t, a, x), Reserved)
	mr.FastForward(DefaultPendingTTL - time.Millisecond)
	expectStatuses(t, reserve(t, b, x), PendingElsewhere)
	mr.FastForward(time.Millisecond)
	expectStatuses(t, reserve(t, b, x), Reserved)
	expectValue(t, mr, x, "p:core-b")
}

func TestMalformedValuesAreTreatedAsAbsentAndLeftAlone(t *testing.T) {
	mr, rdb := newRedis(t)
	sink := &logSink{}
	s := newStore(t, rdb, "core-a", sink)
	values := []string{
		"", "garbage", "p:", "p:a b", "p:a:b", "c:1:2", "c:0:1:5", "c:1:0:5", "c:01:1:5",
		"c:1:1:+5", "c:1:1:5:6", "c:18446744073709551616:1:1", "x:1:1:1",
	}
	keys := make([]Key, 0, len(values)+1)
	for i, v := range values {
		k := key(fmt.Sprintf("m%d", i))
		if err := mr.Set(k.String(), v); err != nil {
			t.Fatalf("seed %q: %v", v, err)
		}
		keys = append(keys, k)
	}
	hash := key("hash")
	mr.HSet(hash.String(), "f", "v")
	keys = append(keys, hash)

	for i, v := range reserve(t, s, keys...) {
		if v.Status != Absent {
			t.Errorf("key %s: status %v, want absent", keys[i], v.Status)
		}
	}
	for i, v := range values {
		expectValue(t, mr, keys[i], v)
	}
	if !mr.Exists(hash.String()) {
		t.Fatal("reserve replaced a key of another type")
	}
	if n := sink.count(malformedMsg); n != len(keys) {
		t.Fatalf("logged %d malformed values, want %d", n, len(keys))
	}
}

func TestEachBatchIsOneRoundTrip(t *testing.T) {
	_, rdb := newRedis(t)
	s := newStore(t, rdb, "core-a", nil)
	reserve(t, s, key("warm"))
	if err := s.Abort(t.Context(), []Key{key("warm")}); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	trips := countRoundTrips(rdb)
	keys := []Key{key("a"), key("b"), key("c"), key("d")}
	reserve(t, s, keys...)
	entries := make([]Entry, len(keys))
	for i, k := range keys {
		entries[i] = Entry{Key: k, Record: sampleRecord}
	}
	if err := s.Commit(t.Context(), entries); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.Abort(t.Context(), keys); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if n := trips.n.Load(); n != 3 {
		t.Fatalf("reserve, commit and abort of 4 keys took %d round trips, want 3", n)
	}
}

func TestEmptyBatchesSkipRedis(t *testing.T) {
	mr, rdb := newRedis(t)
	s := newStore(t, rdb, "core-a", nil)
	mr.Close()
	got, err := s.Reserve(t.Context(), nil)
	if err != nil || got != nil {
		t.Fatalf("Reserve(nil) = %v, %v", got, err)
	}
	if err := s.Commit(t.Context(), nil); err != nil {
		t.Fatalf("Commit(nil) = %v", err)
	}
	if err := s.Abort(t.Context(), nil); err != nil {
		t.Fatalf("Abort(nil) = %v", err)
	}
}
