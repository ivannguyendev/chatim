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

func pushed(t *testing.T, src *fakeSource, evs []*chatimv1.Event, firstSeq, delivered uint64) []*fakeDelivery {
	t.Helper()
	ds := make([]*fakeDelivery, len(evs))
	for i, ev := range evs {
		ds[i] = delivery(t, ev, delivered)
		ds[i].seq = firstSeq + uint64(i)
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

func TestSettleAcksBeforeTheFailureNaksItAndHoldsTheRestUntilItPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		rg.index.failOn(3, errBoom)
		evs := settleEvents()
		ds := pushed(t, rg.src, evs, 1, 3)
		want := []settlement{{}, {acks: 1}, {naks: []time.Duration{2 * time.Minute}}, {}, {}}
		if got := settlements(ds); !sameSettlements(got, want) {
			t.Fatalf("settled %+v, want %+v", got, want)
		}
		if rg.c.Failures() != 1 {
			t.Errorf("failures %d, want the one failed event", rg.c.Failures())
		}
		rg.index.failOn(3, nil)
		again := pushed(t, rg.src, evs[2:3], 3, 4)
		if got := append(settlements(again), settlements(ds[3:])...); !sameSettlements(got, []settlement{{}, {}, {acks: 1}}) {
			t.Fatalf("settled %+v once the failed event passed, want one ack on the last held delivery", got)
		}
		rg.assertLikeOnce(t, evs)
		rg.stop(t)
	})
}

func TestSettleOfARedeliveredTailGivesTheSameRooms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		rg.index.failOn(3, errBoom)
		evs := settleEvents()
		pushed(t, rg.src, evs, 1, 1)
		rg.index.failOn(3, nil)
		tail := pushed(t, rg.src, evs[2:], 3, 2)
		if got := settlements(tail); !sameSettlements(got, acked(2, 3)) {
			t.Fatalf("redelivered tail settled %+v, want one ack on its last delivery", got)
		}
		rg.assertLikeOnce(t, evs)
		rg.stop(t)
	})
}

func TestSettleOfAFailureOnTheFirstEventAcksNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		rg.index.failOn(1, errBoom)
		ds := pushed(t, rg.src, settleEvents()[:2], 1, 1)
		if got := settlements(ds); !sameSettlements(got, []settlement{{naks: []time.Duration{5 * time.Second}}, {}}) {
			t.Fatalf("settled %+v, want the first sent back on the 5s rung and the second held", got)
		}
		rg.stop(t)
	})
}

func TestSettleCountsAGoneEventAsDroppedAndAcksPastIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		rg.index.failOn(2, domain.ErrRoomNotFound)
		ds := pushed(t, rg.src, settleEvents()[:3], 1, 1)
		if got := settlements(ds); !sameSettlements(got, acked(2, 3)) {
			t.Fatalf("settled %+v, want the last delivery acked", got)
		}
		if rg.c.Dropped() != 1 || rg.c.Failures() != 0 {
			t.Errorf("dropped %d failures %d, want the gone event dropped", rg.c.Dropped(), rg.c.Failures())
		}
		rg.stop(t)
	})
}

func TestSettleHoldsAcksAboveAGapUntilTheGapIsHandledOrTheServerFloorCoversIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		evs := settleEvents()
		held := pushed(t, rg.src, evs[3:], 4, 1)
		if got := settlements(held); !sameSettlements(got, make([]settlement, 2)) {
			t.Fatalf("settled %+v above a gap, want nothing acked or sent back", got)
		}
		if rg.src.floorReads() != 2 || rg.sink.Count(gapWarning) != 1 {
			t.Fatalf("floor reads %d, want the lead start and one at the gap, with a warning", rg.src.floorReads())
		}
		gap := pushed(t, rg.src, evs[:3], 1, 2)
		if got := append(settlements(gap), settlements(held)...); !sameSettlements(got, []settlement{{}, {}, {}, {}, {acks: 1}}) {
			t.Fatalf("settled %+v once the gap came back, want one ack on the last held delivery", got)
		}
		rg.src.setFloor(7, nil)
		above := pushed(t, rg.src, evs[:2], 8, 1)
		if got := settlements(above); !sameSettlements(got, acked(1, 2)) {
			t.Fatalf("settled %+v once the server floor covered the gap, want the last acked", got)
		}
		stale := pushed(t, rg.src, evs[:1], 2, 3)
		if got := settlements(stale); !sameSettlements(got, acked(0, 1)) {
			t.Fatalf("settled %+v, want a redelivery below the floor acked again with its newer handle", got)
		}
		rg.stop(t)
	})
}

func TestStopHandsBackDeliveriesHeldAboveAGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newSettleRig(t)
		held := pushed(t, rg.src, settleEvents()[3:], 4, 1)
		rg.stop(t)
		zero := []time.Duration{0}
		if got := settlements(held); !sameSettlements(got, []settlement{{naks: zero}, {naks: zero}}) {
			t.Fatalf("settled %+v at stop, want both handed back with Nak(0) in seq order", got)
		}
	})
}

const gapWarning = "projection holds acks above a missing delivery until the server redelivers it"
