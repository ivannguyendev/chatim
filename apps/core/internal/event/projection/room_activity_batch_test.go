package projection_test

import (
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
)

func TestRoomActivityWritesAFullFetchOfManyRoomsOnce(t *testing.T) {
	const events, rooms = projection.DefaultFetchBatch, 997
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		ds := make([]*fakeDelivery, events)
		for i := range ds {
			room, seq := roomA+uint64(i%rooms), uint64(i/rooms+1)
			ev := msgCreated(room, 0, seq, at(i))
			if i%5 == 0 {
				ev = reactionChanged(room, seq, at(i))
			}
			ds[i] = delivery(t, ev, 1)
		}
		rg.src.push(ds...)
		synctest.Wait()
		got := rg.writer.touched()
		if len(got) != 1 || len(got[0]) != rooms {
			t.Fatalf("touched %d writes; want one write of %d rooms", len(got), rooms)
		}
		for _, a := range got[0] {
			if want := lastSentSeq(events, rooms, int(a.Room-roomA)); a.Seq != want {
				t.Errorf("room %d at seq %d, want %d", a.Room, a.Seq, want)
			}
		}
		if got := settlements(ds); !sameSettlements(got, allAcked(events)) {
			t.Fatalf("want each of %d deliveries acked once", events)
		}
		rg.stop(t)
	})
}

func lastSentSeq(events, rooms, room int) uint64 {
	for i := events - 1 - (events-1-room)%rooms; i >= 0; i -= rooms {
		if i%5 != 0 {
			return uint64(i/rooms + 1)
		}
	}
	return 0
}
