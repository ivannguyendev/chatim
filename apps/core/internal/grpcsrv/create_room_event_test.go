package grpcsrv_test

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type recordingEvents struct {
	mu     sync.Mutex
	rooms  []uint64
	events []*chatimv1.Event
	err    error
}

func (r *recordingEvents) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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

func TestCreateRoomEnqueuesRoomCreated(t *testing.T) {
	clock := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	newID, _ := idSequence(42)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, now: func() time.Time { return clock }, events: events})
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice", "bob"},
	}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	stored, err := rg.rooms.Get(t.Context(), 42)
	if err != nil {
		t.Fatalf("Get(42): %v", err)
	}
	want := pbconv.RoomCreated(stored)
	rooms, got := events.enqueued()
	if !slices.Equal(rooms, []uint64{42}) || len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("enqueued %v %v, want one %v for room 42", rooms, got, want)
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
	if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3}) {
		t.Fatalf("enqueued for rooms %v, want only 3", rooms)
	}
}
