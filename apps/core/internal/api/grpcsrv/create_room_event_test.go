package grpcsrv_test

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type recordingEvents struct {
	mu      sync.Mutex
	rooms   []uint64
	events  []*chatimv1.Event
	batches int
	err     error
}

func (r *recordingEvents) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches++
	for _, ev := range events {
		r.rooms = append(r.rooms, room)
		r.events = append(r.events, ev)
	}
	return r.err
}

func (r *recordingEvents) enqueued() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

func (r *recordingEvents) enqueueCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.batches
}

func TestCreateRoomEnqueuesRoomCreatedAndTheFirstMembers(t *testing.T) {
	clock := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	newID, _ := idSequence(42)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, now: func() time.Time { return clock }, events: events})
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"bob", "alice", "bob", "carol"},
	}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	rooms, got := events.enqueued()
	if want := createdIDs(42, "bob", "alice", "carol"); !slices.Equal(eventIDs(got), want) || events.enqueueCalls() != 1 {
		t.Fatalf("enqueued %v in %d calls, want %v in one", eventIDs(got), events.enqueueCalls(), want)
	}
	if !slices.Equal(rooms, []uint64{42, 42, 42, 42, 42}) {
		t.Fatalf("enqueued for rooms %v, want 42 only", rooms)
	}
	stored, err := rg.rooms.Get(t.Context(), 42)
	if err != nil {
		t.Fatalf("Get(42): %v", err)
	}
	want := []*chatimv1.Event{pbconv.RoomCreated(stored)}
	for _, u := range []string{"bob", "alice", "carol"} {
		m, err := rg.rooms.Member(t.Context(), 42, u)
		if err != nil {
			t.Fatalf("Member(%s): %v", u, err)
		}
		want = append(want, pbconv.MemberEvent(stored.Type, m))
	}
	want = append(want, pbconv.MemberCountChanged(stored, domain.MemberCount{Count: 3, Ver: 1}, "alice", clock))
	for i, ev := range got {
		if !proto.Equal(ev, want[i]) {
			t.Errorf("event %d = %v, want %v", i, ev, want[i])
		}
		if _, ok := ev.GetPayload().(*chatimv1.Event_MemberAdded); i > 0 && i < len(got)-1 && !ok {
			t.Errorf("event %d payload = %T, want member_added", i, ev.GetPayload())
		}
	}
	wantSubjects := []string{"evt.acme.room.42.room_created", "evt.acme.member.42.member_added", "evt.acme.room.42.member_count_changed"}
	for i, ev := range []*chatimv1.Event{got[0], got[1], got[len(got)-1]} {
		msg, err := publish.Message("evt", 42, ev)
		if err != nil || msg.Subject != wantSubjects[i] {
			t.Errorf("subject of event %d = %v, %v; want %s", i, msg, err, wantSubjects[i])
		}
	}
}

func TestCreateRoomSucceedsWhenTheEventIsRefused(t *testing.T) {
	newID, _ := idSequence(7)
	events := &recordingEvents{err: errors.New("publish queue full")}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, events: events})
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"},
	})
	if err != nil || resp.GetRoom().GetId() != "7" {
		t.Fatalf("CreateRoom with a refused event = %v, %v; want room 7", resp, err)
	}
	if _, err := rg.rooms.Get(t.Context(), 7); err != nil {
		t.Fatalf("Get(7): %v", err)
	}
}

func TestCreateRoomEnqueuesOnlyForTheStoredRoom(t *testing.T) {
	newID, _ := idSequence(1, 2, 3)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, events: events})
	occupy(t, rg.rooms, 1, 2)
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"},
	}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Members: []string{"alice"},
	})
	expectCode(t, err, codes.InvalidArgument)
	if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3, 3, 3, 3}) {
		t.Fatalf("enqueued for rooms %v, want only 3", rooms)
	}
}
