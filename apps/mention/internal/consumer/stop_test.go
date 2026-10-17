package consumer_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

func TestStopMidBatchFinishesEventsInFlightAndNaksTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.block = make(chan struct{})
		now := time.Now()
		var ds []*fakeDelivery
		for seq := uint64(1); seq <= 4; seq++ {
			ds = append(ds, delivery(t, created(seq, now, user("Minh", now)), 1))
		}
		q := &fakeQueue{}
		q.push(ds...)
		one := cfg()
		one.Workers = 1
		c := r.consumer(t, q, one)
		run := start(t.Context(), c)
		synctest.Wait()
		closed := make(chan error, 1)
		go func() { closed <- c.Close(t.Context()) }()
		synctest.Wait()
		close(r.resolver.block)
		if err := <-closed; err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := <-run.done; err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := settledOf(ds[0]); got.acks != 1 || len(got.naks) != 0 {
			t.Errorf("event in flight settled %+v, want it finished and acked", got)
		}
		for i, d := range ds[1:] {
			if got := settledOf(d); got.acks != 0 || got.terms != 0 || !slices.Equal(got.naks, []time.Duration{0}) {
				t.Errorf("event %d settled %+v, want exactly one Nak(0)", i+2, got)
			}
		}
	})
}

func TestStopDrainsAFetchInFlightAndHandsItBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		in := delivery(t, mentionMinh(time.Now()), 1)
		src := &expiringSource{held: []jsconsume.Delivery{in}}
		c := r.consumer(t, src, cfg())
		run := start(t.Context(), c)
		synctest.Wait()
		begin := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), cfg().StopTimeout())
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Fatalf("Close within %v = %v", cfg().StopTimeout(), err)
		}
		if took := time.Since(begin); took != cfg().FetchWait {
			t.Errorf("stop took %v, want the fetch read to its %v expiry", took, cfg().FetchWait)
		}
		if err := <-run.done; err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := settledOf(in); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{0}) || r.mentions.count(key(41)) != 0 {
			t.Errorf("settled %+v applied %d, want Nak(0) and no write", got, r.mentions.count(key(41)))
		}
	})
}

func TestRunOnceAndCloseBeforeRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		c := r.consumer(t, &fakeQueue{}, cfg())
		run := start(t.Context(), c)
		synctest.Wait()
		if err := c.Run(t.Context()); err == nil {
			t.Errorf("second Run = nil, want an error")
		}
		run.stop(t)
		if err := r.consumer(t, &fakeQueue{}, cfg()).Close(t.Context()); err != nil {
			t.Errorf("Close before Run = %v", err)
		}
	})
}

func TestCloseDeadlineCancelsEventsInFlightAndNaksThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.block = make(chan struct{})
		defer close(r.resolver.block)
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
		synctest.Wait()
		begin := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want context.DeadlineExceeded", err)
		}
		if took := time.Since(begin); took != time.Second {
			t.Errorf("stop took %v, want the 1s deadline", took)
		}
		if err := <-run.done; err != nil {
			t.Fatalf("Run: %v", err)
		}
		for i, d := range ds {
			if got := settledOf(d); got.acks != 0 || got.terms != 0 || !slices.Equal(got.naks, []time.Duration{0}) {
				t.Errorf("event %d settled %+v, want exactly one Nak(0)", i+1, got)
			}
		}
		if c.Failures() != 0 || r.resolver.busy() != 0 {
			t.Errorf("failures %d resolver busy %d, want none", c.Failures(), r.resolver.busy())
		}
	})
}
