package projection_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var evtConfig = projection.Config{Stream: "CHATIM_EVT", SubjectRoot: subjectRoot}

type consumerRig struct {
	sink   *testlog.Sink
	src    *fakeSource
	writer *fakeWriter
	c      *projection.Consumer
	result chan error
}

func newConsumerRig(t *testing.T, ctx context.Context, extra ...projection.Projection) *consumerRig {
	t.Helper()
	rg := &consumerRig{sink: &testlog.Sink{}, src: newFakeSource(), writer: &fakeWriter{}, result: make(chan error, 1)}
	handlers := append([]projection.Projection{projection.NewRoomActivity(rg.writer)}, extra...)
	rg.c = built(projection.NewConsumer(handlers, rg.src, evtConfig, rg.sink.Logger()))(t)
	go func() { rg.result <- rg.c.Run(ctx) }()
	return rg
}

func (rg *consumerRig) stop(t *testing.T) {
	t.Helper()
	if err := rg.c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-rg.result; err != nil {
		t.Fatalf("Run after Close = %v, want nil", err)
	}
}

func TestConsumerTouchesABatchOfThreeRoomsInOneWriteAndAcksEachDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		time.Sleep(time.Minute)
		ds := []*fakeDelivery{
			delivery(t, msgCreated(roomA, 0, 1, time.Now().Add(-3*time.Second)), 1),
			delivery(t, reactionChanged(roomB, 1, time.Now().Add(-time.Second)), projection.StuckDeliveries),
			delivery(t, msgEdited(roomC, 0, 1, time.Now()), projection.StuckDeliveries+1),
		}
		rg.src.push(ds...)
		synctest.Wait()
		if got := rg.writer.touched(); len(got) != 1 || !slices.EqualFunc(got[0], []uint64{roomA, roomB, roomC}, func(a store.Activity, r uint64) bool { return a.Room == r }) {
			t.Fatalf("touched %v, want one write covering rooms A, B and C", got)
		}
		if got := settlements(ds); !sameSettlements(got, allAcked(3)) {
			t.Errorf("settled %+v; want each delivery acked once", got)
		}
		if rg.c.Lag() != 3*time.Second || rg.c.Stuck() != 2 {
			t.Errorf("lag %v stuck %d; want 3s from the oldest event and the two deliveries at the stuck count", rg.c.Lag(), rg.c.Stuck())
		}
		time.Sleep(projection.DefaultFetchWait)
		synctest.Wait()
		if rg.c.Lag() != 0 || rg.c.Stuck() != 2 || rg.c.Name() != "projections" {
			t.Errorf("after an idle fetch lag %v stuck %d name %q; want lag 0, the stuck count kept, named projections", rg.c.Lag(), rg.c.Stuck(), rg.c.Name())
		}
		rg.stop(t)
	})
}

func TestConsumerTermsGarbageAndAcksKindsNoHandlerKeeps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		ds := []*fakeDelivery{delivery(t, msgCreated(roomB, 0, 1, at(1)), 1), {data: []byte("junk")}, delivery(t, readPositions(roomA, at(1)), 1), {data: []byte("junk")}}
		rg.src.push(ds...)
		synctest.Wait()
		want := []settlement{{acks: 1}, {terms: 1}, {acks: 1}, {terms: 1}}
		if got := settlements(ds); !sameSettlements(got, want) {
			t.Errorf("settled %+v; want events acked and garbage termed", got)
		}
		if rg.c.Dropped() != 2 || rg.c.Failures() != 0 {
			t.Errorf("dropped %d failures %d; want the two undecodable events dropped and the stray kind just acked", rg.c.Dropped(), rg.c.Failures())
		}
		if n := rg.sink.Count("projection dropped an undecodable event"); n != 1 {
			t.Errorf("undecodable warnings %d, want 1 within the second", n)
		}
		rg.stop(t)
	})
}

func TestConsumerNaksEachFailedEventOnItsOwnRetryRung(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		rg.writer.err = errors.New("rooms down")
		ds := []*fakeDelivery{delivery(t, msgCreated(roomA, 0, 1, at(1)), 0), delivery(t, msgCreated(roomB, 0, 1, at(1)), 3)}
		rg.src.push(ds...)
		synctest.Wait()
		want := []settlement{{naks: []time.Duration{5 * time.Second}}, {naks: []time.Duration{2 * time.Minute}}}
		if got := settlements(ds); !sameSettlements(got, want) {
			t.Errorf("settled %+v; want each sent back on the rung of its own delivery count", got)
		}
		if rg.c.Failures() != 2 || rg.c.Dropped() != 0 {
			t.Errorf("failures %d dropped %d; want 2 failures", rg.c.Failures(), rg.c.Dropped())
		}
		asks := rg.src.asks()
		time.Sleep(projection.DefaultFetchWait)
		synctest.Wait()
		if n := rg.src.asks(); n <= asks {
			t.Errorf("fetched %d more times after the failure, want the consumer to keep fetching", n-asks)
		}
		rg.stop(t)
	})
}

func TestConsumerStopFinishesTheRunningBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		held, release := rg.writer.holdNext()
		running := delivery(t, msgCreated(roomA, 0, 1, at(1)), 1)
		rg.src.push(running)
		<-held
		closed := make(chan error, 1)
		go func() { closed <- rg.c.Close(t.Context()) }()
		synctest.Wait()
		rg.src.push(delivery(t, msgCreated(roomB, 0, 1, at(1)), 1))
		close(release)
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if err := <-rg.result; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if acks, _, naks := running.settled(); acks != 1 || len(naks) != 0 {
			t.Errorf("running batch acks %d naks %v, want it finished and acked", acks, naks)
		}
		if len(rg.writer.touched()) != 1 {
			t.Errorf("writes %v; want none after the stop", rg.writer.touched())
		}
	})
}

func TestConsumerStopHandsBackWhatTheLastFetchBrought(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		fetched := []*fakeDelivery{delivery(t, msgCreated(roomB, 0, 1, at(1)), 1), {data: []byte("junk")}}
		rg.src.handOverOnCancel(fetched...)
		synctest.Wait()
		rg.stop(t)
		want := []settlement{{naks: []time.Duration{0}}, {naks: []time.Duration{0}}}
		if got := settlements(fetched); !sameSettlements(got, want) {
			t.Errorf("settled %+v; want each handed back with Nak(0)", got)
		}
		if len(rg.writer.touched()) != 0 || rg.c.Dropped() != 0 {
			t.Errorf("writes %v dropped %d; want nothing handled after the stop", rg.writer.touched(), rg.c.Dropped())
		}
	})
}

func TestConsumerCancelledMidWriteHandsTheBatchBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rg := newConsumerRig(t, ctx)
		held, _ := rg.writer.holdNext()
		d := delivery(t, msgCreated(roomA, 0, 1, at(1)), 2)
		rg.src.push(d)
		<-held
		cancel()
		if err := <-rg.result; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		if acks, _, naks := d.settled(); acks != 0 || !slices.Equal(naks, []time.Duration{0}) {
			t.Errorf("acks %d naks %v; want the event handed back with Nak(0)", acks, naks)
		}
		if rg.c.Failures() != 0 {
			t.Errorf("failures %d, want a cancelled write not counted", rg.c.Failures())
		}
		if err := rg.c.Close(t.Context()); err != nil {
			t.Errorf("Close after Run ended = %v", err)
		}
	})
}
