package consumer_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestStopFinishesEventsInFlightAndNaksTheOnesNotStarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		running, queued, buffered := delivery(t, created(1, now, 1), 1), delivery(t, deleted(1, now), 1), delivery(t, created(2, now, 1), 1)
		two := cfg()
		two.MaxPending = 2
		s := newStream(running, queued, buffered)
		c := r.consumer(t, s, two)
		run := start(t.Context(), c)
		synctest.Wait()
		if _, taken, _, _ := s.stats(); taken != 2 {
			t.Fatalf("took %d events, want 2 with max pending 2", taken)
		}
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
		if got := settledOf(running); got.acks != 1 || len(got.naks) != 0 {
			t.Errorf("event in flight settled %+v, want it finished and acked", got)
		}
		for name, d := range map[string]*fakeDelivery{"queued": queued, "buffered": buffered} {
			if got := settledOf(d); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{0}) {
				t.Errorf("%s event settled %+v, want exactly one Nak(0)", name, got)
			}
		}
		if _, _, left, open := s.stats(); left != 0 || open {
			t.Errorf("left %d open %v, want the stream drained and closed", left, open)
		}
	})
}

func TestCloseDeadlineCancelsEventsInFlightAndNaksThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		defer close(r.sync.block)
		now := time.Now()
		ds := []*fakeDelivery{delivery(t, created(1, now, 1), 1), delivery(t, deleted(1, now), 1), delivery(t, created(2, now, 1), 1)}
		c := r.consumer(t, newStream(ds...), cfg())
		run := start(t.Context(), c)
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Fatalf("Close = %v, want nil once every event in flight was handed back", err)
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
		c := r.consumer(t, newStream(), cfg())
		run := start(t.Context(), c)
		synctest.Wait()
		if err := c.Run(t.Context()); err == nil {
			t.Errorf("second Run = nil, want an error")
		}
		run.stop(t)
		if err := r.consumer(t, newStream(), cfg()).Close(t.Context()); err != nil {
			t.Errorf("Close before Run = %v", err)
		}
	})
}

func TestAFailedOpenIsRetriedAfterAnIdleWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		d := delivery(t, created(1, time.Now(), 0), 1)
		s := newStream(d)
		s.failOpen = errDown
		run := start(t.Context(), r.consumer(t, s, cfg()))
		time.Sleep(3500 * time.Millisecond)
		synctest.Wait()
		s.mu.Lock()
		s.failOpen = nil
		s.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()
		run.stop(t)
		if opens, _, _, _ := s.stats(); opens != 5 || settledOf(d).acks != 1 {
			t.Errorf("opened %d times, settled %+v; want one open per idle wait, then the event acked", opens, settledOf(d))
		}
	})
}
