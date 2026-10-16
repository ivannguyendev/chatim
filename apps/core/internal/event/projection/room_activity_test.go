package projection_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRoomActivityMergesABatchIntoOneWritePerRoom(t *testing.T) {
	w := &fakeWriter{}
	evs := []projection.Event{
		decoded(t, msgCreated(roomA, 0, 3, at(1))),
		decoded(t, msgCreated(roomA, 0, 5, at(4))),
		decoded(t, msgCreated(roomA, 0, 4, at(6))),
		decoded(t, reactionChanged(roomB, 2, at(2))),
		decoded(t, msgEdited(roomC, 0, 1, at(3))),
	}
	errs := projection.NewRoomActivity(w).Handle(t.Context(), evs)
	for i, err := range errs {
		if err != nil {
			t.Errorf("event %d: %v", i, err)
		}
	}
	want := []store.Activity{
		{Room: roomA, Seq: 5, At: at(6)},
		{Room: roomB, At: at(2)},
		{Room: roomC, At: at(3), Edited: true},
	}
	got := w.touched()
	if len(got) != 1 || len(got[0]) != len(want) {
		t.Fatalf("touched %v, want one write of %v", got, want)
	}
	for i := range want {
		if got[0][i] != want[i] {
			t.Errorf("activity %d = %v, want %v", i, got[0][i], want[i])
		}
	}
}

func TestRoomActivityMergesEveryKindOfARoomThreadIntoOneRow(t *testing.T) {
	w := &fakeWriter{}
	evs := []projection.Event{
		decoded(t, reactionChanged(roomA, 2, at(9))),
		decoded(t, msgCreated(roomA, 0, 3, at(1))),
		decoded(t, msgEdited(roomA, 0, 1, at(5))),
		decoded(t, msgCreated(roomA, 7, 1, at(8))),
		decoded(t, msgCreated(roomA, 0, 4, at(2))),
		decoded(t, readUpdated(roomA, at(10))),
		decoded(t, reactionChanged(roomB, 1, at(3))),
		decoded(t, reactionChanged(roomB, 2, at(6))),
		decoded(t, reactionChanged(roomB, 3, at(4))),
	}
	projection.NewRoomActivity(w).Handle(t.Context(), evs)
	want := []store.Activity{
		{Room: roomA, Seq: 4, At: at(2), Edited: true},
		{Room: roomA, Thread: 7, Seq: 1, At: at(8)},
		{Room: roomB, At: at(6)},
	}
	if got := w.touched(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("touched %v, want one write of %v", got, want)
	}
}

func TestRoomActivityFailsEveryEventOfAFailedWrite(t *testing.T) {
	errDown := errors.New("rooms down")
	w := &fakeWriter{err: errDown}
	errs := projection.NewRoomActivity(w).Handle(t.Context(), []projection.Event{
		decoded(t, msgCreated(roomA, 0, 1, at(1))),
		decoded(t, readUpdated(roomB, at(1))),
		decoded(t, msgCreated(roomC, 0, 1, at(1))),
	})
	if !errors.Is(errs[0], errDown) || !errors.Is(errs[2], errDown) || errors.Is(errs[1], errDown) {
		t.Errorf("errors %v; want the write error on both touched events only", errs)
	}
}

func TestRoomActivityGivesTheSameRoomsForDuplicatedAndReorderedEvents(t *testing.T) {
	inOrder := []*chatimv1.Event{
		msgCreated(roomA, 0, 1, at(1)), msgCreated(roomA, 0, 2, at(2)), msgEdited(roomA, 0, 1, at(3)),
		reactionChanged(roomB, 1, at(4)), msgCreated(roomB, 7, 1, at(5)), msgCreated(roomC, 0, 1, at(6)),
	}
	shuffled := []*chatimv1.Event{inOrder[5], inOrder[2], inOrder[1], inOrder[4], inOrder[0], inOrder[3], inOrder[1], inOrder[2]}
	want := roomsAfter(t, [][]*chatimv1.Event{inOrder})
	if a, b := want[roomA], want[roomB]; a != (roomTrace{seq: 2, msgAt: at(2), changed: true, edited: true}) || b != (roomTrace{changed: true}) {
		t.Fatalf("in-order rooms A %+v B %+v; want A at seq 2 sent at 2s and edited, B changed with no main seq", a, b)
	}
	for name, batches := range map[string][][]*chatimv1.Event{
		"one shuffled batch with duplicates": {shuffled},
		"split, newest batch first":          {shuffled[:3], shuffled[3:6], shuffled[6:]},
		"every event twice":                  {inOrder, inOrder},
	} {
		got := roomsAfter(t, batches)
		for id, r := range want {
			if got[id] != r {
				t.Errorf("%s: room %d = %+v, want %+v", name, id, got[id], r)
			}
		}
	}
}

type roomTrace struct {
	seq             uint64
	msgAt           time.Time
	changed, edited bool
}

func traceOf(r domain.Room) roomTrace {
	return roomTrace{seq: r.LastSeq, msgAt: r.LastMsgAt, changed: !r.LastChangeAt.IsZero(), edited: !r.LastEditAt.IsZero()}
}

func roomsAfter(t *testing.T, batches [][]*chatimv1.Event) map[uint64]roomTrace {
	t.Helper()
	rooms := memstore.NewRooms()
	out := make(map[uint64]roomTrace)
	for _, id := range []uint64{roomA, roomB, roomC} {
		if err := rooms.InsertRoom(t.Context(), domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, CreatedAt: baseTime}); err != nil {
			t.Fatalf("InsertRoom: %v", err)
		}
	}
	p := projection.NewRoomActivity(rooms)
	for _, b := range batches {
		evs := make([]projection.Event, len(b))
		for i, ev := range b {
			evs[i] = decoded(t, ev)
		}
		for _, err := range p.Handle(t.Context(), evs) {
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
		}
	}
	for _, id := range []uint64{roomA, roomB, roomC} {
		r, err := rooms.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		out[id] = traceOf(r)
	}
	return out
}
