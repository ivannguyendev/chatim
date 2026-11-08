package consumer_test

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestFileLessEventsDoNotWaitForASlowCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.slow = map[uint64]time.Duration{1: 10 * time.Minute}
		now := time.Now()
		slow := delivery(t, created(1, now, 1), 1)
		s := newStream(slow)
		var fast []*fakeDelivery
		for seq := uint64(2); seq <= 200; seq++ {
			fast = append(fast, delivery(t, created(seq, now, 0), 1))
		}
		s.push(fast...)
		run := start(t.Context(), r.consumer(t, s, cfg()))
		synctest.Wait()
		for i, d := range fast {
			if got := settledOf(d); got.acks != 1 {
				t.Fatalf("event %d settled %+v at once, want acked while the copy runs", i+2, got)
			}
		}
		if got := settledOf(slow); got.acks+len(got.naks) != 0 || r.sync.inFlight() != 1 {
			t.Fatalf("slow settled %+v with %d in flight, want it still running alone", got, r.sync.inFlight())
		}
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		run.stop(t)
		if got := settledOf(slow); got.acks != 1 {
			t.Fatalf("slow settled %+v, want acked after its copy", got)
		}
	})
}

func TestASlowEventHoldsOnlyItsOwnMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.slow = map[uint64]time.Duration{1: time.Minute}
		now := time.Now()
		create, remove, other := delivery(t, created(1, now, 1), 1), delivery(t, deleted(1, now), 1), delivery(t, created(2, now, 1), 1)
		run := start(t.Context(), r.consumer(t, newStream(create, remove, other), cfg()))
		synctest.Wait()
		if a, b, c := settledOf(create), settledOf(remove), settledOf(other); a.acks != 0 || b.acks != 0 || c.acks != 1 {
			t.Fatalf("create %+v delete %+v other %+v, want only the other message done", a, b, c)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		run.stop(t)
		if a, b := settledOf(create), settledOf(remove); a.acks != 1 || b.acks != 1 {
			t.Fatalf("create %+v delete %+v, want both acked after the slow one", a, b)
		}
		calls := r.sync.recorded()
		if len(calls) != 3 || calls[2].ev.Ref.Seq != 1 {
			t.Fatalf("calls %+v, want the delete of seq 1 last", calls)
		}
	})
}

func TestMaxPendingBoundsTheEventsTaken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		var ds []*fakeDelivery
		for seq := uint64(1); seq <= 5; seq++ {
			ds = append(ds, delivery(t, created(seq, now, 1), 1))
		}
		three := cfg()
		three.MaxPending = 3
		s := newStream(ds...)
		run := start(t.Context(), r.consumer(t, s, three))
		synctest.Wait()
		if _, taken, left, _ := s.stats(); taken != 3 || left != 2 || r.sync.inFlight() != 3 {
			t.Fatalf("took %d left %d in flight %d, want 3 taken and 2 waiting", taken, left, r.sync.inFlight())
		}
		close(r.sync.block)
		synctest.Wait()
		run.stop(t)
		for i, d := range ds {
			if got := settledOf(d); got.acks != 1 {
				t.Errorf("event %d settled %+v, want acked", i+1, got)
			}
		}
	})
}
