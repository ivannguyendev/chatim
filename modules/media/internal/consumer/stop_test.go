package consumer_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestStopMidBatchFinishesEventsInFlightAndNaksTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		var ds []*fakeDelivery
		for seq := uint64(1); seq <= 4; seq++ {
			ds = append(ds, delivery(t, created(seq, now, 1), 1))
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
		close(r.sync.block)
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
			if got := settledOf(d); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{0}) {
				t.Errorf("event %d settled %+v, want exactly one Nak(0)", i+2, got)
			}
		}
	})
}

func TestCloseDeadlineCancelsEventsInFlightAndNaksThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		defer close(r.sync.block)
		now := time.Now()
		var ds []*fakeDelivery
		for seq := uint64(1); seq <= 3; seq++ {
			ds = append(ds, delivery(t, created(seq, now, 1), 1))
		}
		q := &fakeQueue{}
		q.push(ds...)
		one := cfg()
		one.Workers = 1
		c := r.consumer(t, q, one)
		run := start(t.Context(), c)
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want context.DeadlineExceeded", err)
		}
		if err := <-run.done; err != nil {
			t.Fatalf("Run: %v", err)
		}
		for i, d := range ds {
			if got := settledOf(d); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{0}) {
				t.Errorf("event %d settled %+v, want exactly one Nak(0)", i+1, got)
			}
		}
		if c.Stuck() != 0 || r.sync.inFlight() != 0 {
			t.Errorf("stuck %d in flight %d", c.Stuck(), r.sync.inFlight())
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

func TestWorkersSyncEventsOfABatchConcurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		q := &fakeQueue{}
		q.push(delivery(t, created(1, now, 1), 1), delivery(t, created(2, now, 1), 1), delivery(t, created(3, now, 1), 1))
		two := cfg()
		two.Workers = 2
		run := start(t.Context(), r.consumer(t, q, two))
		synctest.Wait()
		if got := r.sync.inFlight(); got != 2 {
			t.Errorf("events in flight %d, want the 2 workers busy", got)
		}
		close(r.sync.block)
		synctest.Wait()
		run.stop(t)
		if got := len(r.sync.recorded()); got != 3 {
			t.Errorf("synced %d events, want 3", got)
		}
	})
}
