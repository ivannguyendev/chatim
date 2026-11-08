package consumer_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestNothingIsTakenUntilMediaIsReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		var ready atomic.Bool
		r.ready = ready.Load
		d := delivery(t, created(1, time.Now(), 1), 1)
		s := newStream(d)
		run := start(t.Context(), r.consumer(t, s, cfg()))
		time.Sleep(time.Minute)
		synctest.Wait()
		if opens, taken, _, _ := s.stats(); opens != 0 || taken != 0 || len(r.sync.recorded()) != 0 {
			t.Fatalf("opened %d took %d synced %d while not ready", opens, taken, len(r.sync.recorded()))
		}
		ready.Store(true)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		run.stop(t)
		if got := settledOf(d); got.acks != 1 {
			t.Fatalf("settled %+v once ready, want one ack", got)
		}
	})
}

func TestLosingReadinessDrainsTheStreamWithoutNaksAndReopensLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		var ready atomic.Bool
		ready.Store(true)
		r.ready = ready.Load
		now := time.Now()
		first, later := delivery(t, created(1, now, 0), 1), delivery(t, created(2, now, 1), 1)
		s := newStream(first)
		run := start(t.Context(), r.consumer(t, s, cfg()))
		synctest.Wait()
		ready.Store(false)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if opens, _, _, open := s.stats(); opens != 1 || open {
			t.Fatalf("opened %d still open %v, want the one stream drained and closed", opens, open)
		}
		s.push(later)
		time.Sleep(time.Minute)
		synctest.Wait()
		if _, taken, left, _ := s.stats(); taken != 1 || left != 1 {
			t.Fatalf("took %d left %d while not ready, want the later event untouched", taken, left)
		}
		ready.Store(true)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		run.stop(t)
		opens, _, _, _ := s.stats()
		if a, b := settledOf(first), settledOf(later); a.acks != 1 || b.acks != 1 || len(a.naks)+len(b.naks) != 0 || opens != 2 {
			t.Fatalf("first %+v later %+v opens %d, want both acked once, no nak, two opens", a, b, opens)
		}
	})
}
