package consumer_test

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestTwoConsumersShareDeliveriesAndProcessEachOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.block = make(chan struct{})
		now := time.Now()
		q := &fakeQueue{}
		var ds []*fakeDelivery
		for seq := uint64(1); seq <= 6; seq++ {
			ds = append(ds, delivery(t, created(seq, now, user("Minh", now)), 1))
		}
		q.push(ds...)
		one := cfg()
		one.FetchBatch, one.Workers = 1, 1
		a, b := start(t.Context(), r.consumer(t, q, one)), start(t.Context(), r.consumer(t, q, one))
		synctest.Wait()
		if got := r.resolver.busy(); got != 2 {
			t.Fatalf("events in flight %d, want one per consumer", got)
		}
		close(r.resolver.block)
		synctest.Wait()
		a.stop(t)
		b.stop(t)
		for i, d := range ds {
			if got := settledOf(d); got.acks != 1 || got.terms != 0 || len(got.naks) != 0 {
				t.Errorf("delivery %d settled %+v, want one ack", i+1, got)
			}
			if n := r.mentions.count(key(uint64(i + 1))); n != 1 {
				t.Errorf("seq %d applied %d times, want once", i+1, n)
			}
		}
	})
}

func TestWorkersProcessEventsOfABatchConcurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.block = make(chan struct{})
		now := time.Now()
		q := &fakeQueue{}
		q.push(delivery(t, created(1, now, user("Minh", now)), 1), delivery(t, created(2, now, user("Hoa", now)), 1), delivery(t, created(3, now, user("Hoa", now)), 1))
		two := cfg()
		two.Workers = 2
		run := start(t.Context(), r.consumer(t, q, two))
		synctest.Wait()
		if got := r.resolver.busy(); got != 2 {
			t.Errorf("events in flight %d, want the 2 workers busy", got)
		}
		close(r.resolver.block)
		synctest.Wait()
		run.stop(t)
		if got := len(r.js.published()); got != 3 {
			t.Errorf("published %d notifications, want 3", got)
		}
	})
}
