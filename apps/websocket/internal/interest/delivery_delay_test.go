package interest

import (
	"testing"
	"time"
)

func TestADeliveredEventRecordsItsDelayFromTheStoreTime(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		env := msg("t1", 1)
		env.StoredAt = time.Now().Add(-40 * time.Millisecond)
		f.route(env)
		if len(s.take()) != 1 {
			t.Fatal("the event did not reach the connection")
		}
		h := f.table.stats.delivery.read()
		if h.Count != 1 || h.Sum != 0.04 {
			t.Fatalf("delivery seconds count %d sum %v, want 1 and 0.04", h.Count, h.Sum)
		}
		if h.Cumulative[2] != 0 || h.Cumulative[3] != 1 {
			t.Fatalf("delivery buckets %v, want the sample in the 0.05 bucket", h.Cumulative)
		}
	})
}

func TestAnEventDeliveredWhileCatchingUpStillRecordsItsDelay(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		env := msg("t1", 1)
		env.CatchingUp = true
		env.StoredAt = time.Now().Add(-3 * time.Second)
		f.route(env)
		if len(s.take()) != 1 {
			t.Fatal("the event did not reach the connection")
		}
		if h := f.table.stats.delivery.read(); h.Count != 1 || h.Sum != 3 {
			t.Fatalf("delivery seconds count %d sum %v, want 1 and 3", h.Count, h.Sum)
		}
	})
}

func TestAnEventWithNoRecipientRecordsNoDelay(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 2)
		s.take()
		env := msg("t1", 1)
		env.StoredAt = time.Now().Add(-time.Second)
		f.route(env)
		if h := f.table.stats.delivery.read(); h.Count != 0 {
			t.Fatalf("delivery seconds count %d, want 0", h.Count)
		}
	})
}

func TestAnEventWithoutAStoreTimeRecordsNoDelay(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		f.route(msg("t1", 1))
		if len(s.take()) != 1 {
			t.Fatal("the event did not reach the connection")
		}
		if h := f.table.stats.delivery.read(); h.Count != 0 {
			t.Fatalf("delivery seconds count %d, want 0", h.Count)
		}
	})
}
