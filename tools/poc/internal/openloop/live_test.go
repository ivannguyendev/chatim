package openloop

import (
	"testing"
	"time"
)

func TestCIDsCarryTheirShotIndexForOneRunOnly(t *testing.T) {
	cid := CID("r1a2", 12345)
	if cid != "r1a2-12345" {
		t.Fatalf("CID = %q", cid)
	}
	if i, ok := ShotOf("r1a2", cid); !ok || i != 12345 {
		t.Fatalf("ShotOf(%q) = %d, %v", cid, i, ok)
	}
	for _, other := range []string{"r1a3-12345", "r1a2-", "r1a2-x1", "r1a2--4", "r1a2"} {
		if _, ok := ShotOf("r1a2", other); ok {
			t.Fatalf("ShotOf accepted %q", other)
		}
	}
}

func TestLiveRecordsLagOfMeasuredEventsOnceEach(t *testing.T) {
	l := NewLive(10)
	base := time.Unix(1_700_000_000, 0)
	for range 3 {
		l.Expect()
	}
	l.Observe(3, base, base.Add(time.Second))
	l.Observe(10, base, base.Add(4*time.Millisecond))
	l.Observe(11, base, base.Add(6*time.Millisecond))
	l.Observe(11, base, base.Add(9*time.Millisecond))
	if l.Complete() {
		t.Fatal("Complete with 2 of 3 expected events")
	}
	l.Observe(12, base, base.Add(2*time.Millisecond))
	if !l.Complete() {
		t.Fatal("not Complete after every expected event")
	}
	got := l.Summary()
	if got.Expected != 3 || got.Received != 3 || got.Duplicates != 1 {
		t.Fatalf("summary = %+v, want 3 of 3 with 1 duplicate", got)
	}
	if got.Lag.Count != 3 || got.Lag.P50 != 4*time.Millisecond || got.Lag.Max != 6*time.Millisecond {
		t.Fatalf("lag = %+v, want first deliveries only, warmup excluded", got.Lag)
	}
}
