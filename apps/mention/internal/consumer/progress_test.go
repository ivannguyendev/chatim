package consumer_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/consumer"
)

func TestSlowBatchStaysInProgressAndSettlesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.delay = consumer.AckWait*2/3 + 2*time.Second
		now := time.Now()
		var ds []*fakeDelivery
		for seq := uint64(1); seq <= 3; seq++ {
			ds = append(ds, delivery(t, created(seq, now, user("Minh", now)), 1))
		}
		q := &fakeQueue{}
		q.push(ds...)
		one := cfg()
		one.Workers = 1
		c := r.consumer(t, q, one)
		run := start(t.Context(), c)
		time.Sleep(3*r.resolver.delay + time.Second)
		synctest.Wait()
		every := consumer.AckWait / 3
		for i, d := range ds {
			calls, late := d.progressed()
			want := int(time.Duration(i+1) * r.resolver.delay / every)
			if calls != want || late != 0 {
				t.Errorf("event %d in progress %d times (%d after settle), want %d and none late", i+1, calls, late, want)
			}
			if got := settledOf(d); got.acks != 1 || got.terms != 0 || len(got.naks) != 0 {
				t.Errorf("event %d settled %+v, want exactly one ack", i+1, got)
			}
		}
		time.Sleep(consumer.AckWait)
		synctest.Wait()
		for i, d := range ds {
			if calls, late := d.progressed(); late != 0 {
				t.Errorf("event %d got %d in progress calls after settle (total %d), want the ticker stopped", i+1, late, calls)
			}
		}
		run.stop(t)
	})
}
