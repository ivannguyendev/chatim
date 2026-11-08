package consumer_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestNothingIsFetchedUntilMediaIsReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		var ready atomic.Bool
		r.ready = ready.Load
		d := delivery(t, created(1, time.Now(), 1), 1)
		q := &fakeQueue{}
		q.push(d)
		run := start(t.Context(), r.consumer(t, q, cfg()))
		time.Sleep(time.Minute)
		synctest.Wait()
		if q.fetches() != 0 || len(r.sync.recorded()) != 0 {
			t.Fatalf("fetched %d times and synced %d while not ready", q.fetches(), len(r.sync.recorded()))
		}
		if got := settledOf(d); got.acks+got.terms+len(got.naks) != 0 {
			t.Fatalf("settled %+v while not ready", got)
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
