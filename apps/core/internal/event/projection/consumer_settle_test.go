package projection_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type settleRig struct {
	rooms  *memstore.Rooms
	index  *fakeHandler
	src    *fakeSource
	sink   *testlog.Sink
	c      *projection.Consumer
	result chan error
}

func newSettleRig(t *testing.T) *settleRig {
	t.Helper()
	rg := &settleRig{rooms: memstore.NewRooms(), index: newFakeHandler("index", keepAll), src: newFakeSource(), sink: &testlog.Sink{}, result: make(chan error, 1)}
	for _, id := range []uint64{roomA, roomB, roomC} {
		if err := rg.rooms.InsertRoom(t.Context(), domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, CreatedAt: baseTime}); err != nil {
			t.Fatalf("InsertRoom: %v", err)
		}
	}
	hs := []projection.Projection{projection.NewRoomActivity(rg.rooms), rg.index}
	rg.c = built(projection.NewConsumer(hs, rg.src, evtConfig, rg.sink.Logger()))(t)
	go func() { rg.result <- rg.c.Run(t.Context()) }()
	synctest.Wait()
	return rg
}

func (rg *settleRig) stop(t *testing.T) {
	t.Helper()
	if err := rg.c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-rg.result; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func (rg *settleRig) trace(t *testing.T) map[uint64]roomTrace {
	t.Helper()
	out := make(map[uint64]roomTrace)
	for _, id := range []uint64{roomA, roomB, roomC} {
		r, err := rg.rooms.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		out[id] = traceOf(r)
	}
	return out
}

func settleEvents() []*chatimv1.Event {
	return []*chatimv1.Event{
		msgCreated(roomA, 0, 1, at(1)), msgCreated(roomB, 0, 2, at(2)), msgCreated(roomA, 0, 3, at(3)),
		msgEdited(roomC, 0, 4, at(4)), msgCreated(roomB, 0, 5, at(5)),
	}
}

func pushed(t *testing.T, src *fakeSource, evs []*chatimv1.Event, delivered uint64) []*fakeDelivery {
	t.Helper()
	ds := make([]*fakeDelivery, len(evs))
	for i, ev := range evs {
		ds[i] = delivery(t, ev, delivered)
	}
	src.push(ds...)
	synctest.Wait()
	return ds
}

func (rg *settleRig) assertLikeOnce(t *testing.T, evs []*chatimv1.Event) {
	t.Helper()
	control := roomsAfter(t, [][]*chatimv1.Event{evs})
	for id, r := range rg.trace(t) {
		if r != control[id] {
			t.Errorf("room %d = %+v after the redelivery, want %+v as if handled once", id, r, control[id])
		}
	}
}

func TestSettleNaksOnlyTheFailedEventOnItsRungAndAcksTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		rg.index.failOn(3, errBoom)
		evs := settleEvents()
		ds := pushed(t, rg.src, evs, 3)
		want := allAcked(len(ds))
		want[2] = settlement{naks: []time.Duration{2 * time.Minute}}
		if got := settlements(ds); !sameSettlements(got, want) {
			t.Fatalf("settled %+v, want %+v", got, want)
		}
		if rg.c.Failures() != 1 || rg.c.Dropped() != 0 {
			t.Errorf("failures %d dropped %d, want the one failed event", rg.c.Failures(), rg.c.Dropped())
		}
		rg.index.failOn(3, nil)
		again := pushed(t, rg.src, evs[2:3], 4)
		if got := settlements(again); !sameSettlements(got, allAcked(1)) {
			t.Fatalf("redelivery settled %+v, want it acked once it passes", got)
		}
		rg.assertLikeOnce(t, evs)
		rg.stop(t)
	})
}

func TestSettleCountsAGoneEventAsDroppedAndAcksIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		rg.index.failOn(2, domain.ErrRoomNotFound)
		ds := pushed(t, rg.src, settleEvents()[:3], 1)
		if got := settlements(ds); !sameSettlements(got, allAcked(3)) {
			t.Fatalf("settled %+v, want every delivery acked", got)
		}
		if rg.c.Dropped() != 1 || rg.c.Failures() != 0 {
			t.Errorf("dropped %d failures %d, want the gone event dropped", rg.c.Dropped(), rg.c.Failures())
		}
		rg.stop(t)
	})
}
