package projection_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
)

func TestConsumersFetchWithTheirProjectionSettings(t *testing.T) {
	reads := newReadRig(t).read
	for name, tc := range map[string]struct {
		p    projection.Projection
		cfg  projection.Config
		want fetchAsk
	}{
		"room activity by default": {projection.NewRoomActivity(&fakeWriter{}), evtConfig, fetchAsk{8192, 2500 * time.Millisecond}},
		"mention read by default":  {reads, evtConfig, fetchAsk{256, 500 * time.Millisecond}},
		"room activity configured": {projection.NewRoomActivity(&fakeWriter{}), tuned, fetchAsk{2048, 2 * time.Second}},
		"mention read configured":  {reads, tuned, fetchAsk{128, 250 * time.Millisecond}},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				src := newFakeSource()
				c := built(projection.NewConsumer(tc.p, src, tc.cfg, (&testlog.Sink{}).Logger()))(t)
				done := make(chan error, 1)
				go func() { done <- c.Run(t.Context()) }()
				synctest.Wait()
				if got, ok := src.firstAsk(); !ok || got != tc.want {
					t.Errorf("first fetch %+v (asked %v), want %+v", got, ok, tc.want)
				}
				if err := c.Close(t.Context()); err != nil {
					t.Fatalf("Close: %v", err)
				}
				<-done
			})
		})
	}
}

var tuned = projection.Config{
	Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: 128, FetchWait: 250 * time.Millisecond,
	ActivityFetchBatch: 2048, ActivityFetchWait: 2 * time.Second,
}

func TestRoomActivityWritesAFullFetchOfManyRoomsOnce(t *testing.T) {
	const events, rooms = projection.DefaultActivityFetchBatch, 997
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
		for i, d := range ds {
			if acks, _, _ := d.settled(); acks != 1 {
				t.Fatalf("delivery %d acked %d times, want once", i, acks)
			}
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
