package consumer_test

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestASlowSyncStaysInProgressAndASettledOneGetsNone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.slow = map[uint64]time.Duration{1: 3 * cfg().AckWait}
		now := time.Now()
		slow, fast := delivery(t, created(1, now, 1), 1), delivery(t, created(2, now, 1), 1)
		for _, d := range []*fakeDelivery{slow, fast} {
			d.ackWait, d.touched = cfg().AckWait, now
		}
		q := &fakeQueue{}
		q.push(slow, fast)
		run := start(t.Context(), r.consumer(t, q, cfg()))
		synctest.Wait()
		if got := settledOf(fast); got.acks != 1 {
			t.Fatalf("fast settled %+v, want acked at once", got)
		}
		time.Sleep(3*cfg().AckWait + time.Second)
		synctest.Wait()
		run.stop(t)
		calls, late := slow.progressed()
		if got := settledOf(slow); got.acks != 1 || calls < 8 || late != 0 || slow.redelivered() {
			t.Fatalf("slow settled %+v in progress %d late %d redelivered %v", got, calls, late, slow.redelivered())
		}
		if calls, _ := fast.progressed(); calls != 0 || fast.redelivered() {
			t.Fatalf("settled delivery got %d in progress signals", calls)
		}
	})
}
