package projection_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type avatarSet struct {
	room, ver uint64
	users     []string
}

type fakeAvatars struct {
	rooms    map[uint64]domain.Room
	samples  map[uint64][]string
	setErr   map[uint64]error
	infosErr error
	infos    [][]uint64
	sampled  []uint64
	sets     []avatarSet
}

func newFakeAvatars() *fakeAvatars {
	return &fakeAvatars{rooms: map[uint64]domain.Room{}, samples: map[uint64][]string{}, setErr: map[uint64]error{}}
}

func (f *fakeAvatars) RoomInfos(_ context.Context, rooms []uint64) ([]domain.Room, error) {
	f.infos = append(f.infos, rooms)
	if len(rooms) > store.RoomActivityBatch {
		return nil, apperr.ErrInvalidArgument
	}
	var out []domain.Room
	for _, id := range rooms {
		if r, ok := f.rooms[id]; ok {
			out = append(out, r)
		}
	}
	return out, f.infosErr
}

func (f *fakeAvatars) MemberSample(_ context.Context, room uint64, limit int) ([]string, error) {
	f.sampled = append(f.sampled, room)
	s := f.samples[room]
	return s[:min(len(s), limit)], nil
}

func (f *fakeAvatars) SetAvatarUsers(_ context.Context, room, ver uint64, next []string) error {
	f.sets = append(f.sets, avatarSet{room: room, ver: ver, users: next})
	return f.setErr[room]
}

func (f *fakeAvatars) group(room uint64, ver uint64, users ...string) {
	f.rooms[room] = domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, AvatarUsers: users, AvatarVer: ver}
}

func memberEvent(t *testing.T, room uint64, removed bool, kind chatimv1.RoomType) projection.Event {
	t.Helper()
	ev := envelope(room, 0, 0, at(1))
	ev.RoomType = kind
	if removed {
		ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{User: "x"}}
	} else {
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{User: "x"}}
	}
	return decoded(t, ev)
}

func added(t *testing.T, room uint64) projection.Event {
	return memberEvent(t, room, false, chatimv1.RoomType_ROOM_TYPE_GROUP)
}

func removedFrom(t *testing.T, room uint64) projection.Event {
	return memberEvent(t, room, true, chatimv1.RoomType_ROOM_TYPE_GROUP)
}

func handleAvatars(t *testing.T, f *fakeAvatars, evs ...projection.Event) []error {
	t.Helper()
	errs := projection.NewAvatarUsers(f).Handle(t.Context(), evs)
	if len(errs) != len(evs) {
		t.Fatalf("Handle gave %d results for %d events", len(errs), len(evs))
	}
	return errs
}

func allNil(t *testing.T, errs []error) {
	t.Helper()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
}

func TestAvatarUsersKeepsOnlyGroupMemberAddsAndRemovals(t *testing.T) {
	h := projection.NewAvatarUsers(newFakeAvatars())
	if h.Name() != projection.AvatarUsersName {
		t.Fatalf("name = %q", h.Name())
	}
	if !h.Keeps(added(t, roomA)) || !h.Keeps(removedFrom(t, roomA)) {
		t.Fatal("group adds and removals are not kept")
	}
	if h.Keeps(memberEvent(t, roomA, false, chatimv1.RoomType_ROOM_TYPE_DM)) || h.Keeps(memberEvent(t, roomA, true, chatimv1.RoomType_ROOM_TYPE_DM)) {
		t.Fatal("DM member events are kept")
	}
	for _, c := range kindCases {
		if c.name == "member_added" || c.name == "member_removed" {
			continue
		}
		ev := kindEvent(t, c, 0)
		ev.RoomType = chatimv1.RoomType_ROOM_TYPE_GROUP
		if h.Keeps(decoded(t, ev)) {
			t.Errorf("keeps %s", c.name)
		}
	}
}

func TestAnAddToASmallGroupWritesTheSample(t *testing.T) {
	f := newFakeAvatars()
	f.group(roomA, 1, "an")
	f.samples[roomA] = []string{"an", "binh"}
	allNil(t, handleAvatars(t, f, added(t, roomA)))
	if want := []avatarSet{{room: roomA, ver: 1, users: []string{"an", "binh"}}}; !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("sets = %v, want %v", f.sets, want)
	}
}

func TestAnAddToAFullListReadsNoMembers(t *testing.T) {
	f := newFakeAvatars()
	f.group(roomA, 3, "an", "binh", "chi", "dung")
	allNil(t, handleAvatars(t, f, added(t, roomA), added(t, roomA)))
	if len(f.sampled) != 0 || len(f.sets) != 0 {
		t.Fatalf("sampled %v, sets %v", f.sampled, f.sets)
	}
}

func TestAnAddGivingTheSameUsersWritesNothing(t *testing.T) {
	f := newFakeAvatars()
	f.group(roomA, 2, "binh", "an")
	f.samples[roomA] = []string{"an", "binh"}
	allNil(t, handleAvatars(t, f, added(t, roomA)))
	if len(f.sampled) != 1 || len(f.sets) != 0 {
		t.Fatalf("sampled %v, sets %v", f.sampled, f.sets)
	}
}

func TestARemovalAlwaysWritesAndBumpsTheVer(t *testing.T) {
	f := newFakeAvatars()
	f.group(roomA, 5, "an", "binh", "chi", "dung")
	f.samples[roomA] = []string{"an", "binh", "dung", "em"}
	f.group(roomB, 7, "an", "binh", "chi", "dung")
	f.samples[roomB] = []string{"dung", "chi", "binh", "an"}
	allNil(t, handleAvatars(t, f, removedFrom(t, roomA), added(t, roomB), removedFrom(t, roomB)))
	want := []avatarSet{
		{room: roomA, ver: 5, users: []string{"an", "binh", "dung", "em"}},
		{room: roomB, ver: 7, users: []string{"dung", "chi", "binh", "an"}},
	}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("sets = %v, want %v", f.sets, want)
	}
}

func TestMissingRoomsAndDirectRoomsAreSkipped(t *testing.T) {
	f := newFakeAvatars()
	f.rooms[roomB] = domain.Room{ID: roomB, Tenant: tenant, Type: domain.RoomDM}
	allNil(t, handleAvatars(t, f, added(t, roomA), removedFrom(t, roomB)))
	if len(f.sampled) != 0 || len(f.sets) != 0 {
		t.Fatalf("sampled %v, sets %v", f.sampled, f.sets)
	}
}

func TestALostWriteFailsOnlyThatRoomsEvents(t *testing.T) {
	f := newFakeAvatars()
	f.group(roomA, 1)
	f.samples[roomA] = []string{"an"}
	f.group(roomB, 1)
	f.samples[roomB] = []string{"binh"}
	f.setErr[roomA] = domain.ErrRetryLater
	errs := handleAvatars(t, f, added(t, roomA), added(t, roomB), removedFrom(t, roomA))
	if !errors.Is(errs[0], domain.ErrRetryLater) || errs[1] != nil || !errors.Is(errs[2], domain.ErrRetryLater) {
		t.Fatalf("errs = %v", errs)
	}
}

func TestARoomReadFailureFailsEveryEvent(t *testing.T) {
	f := newFakeAvatars()
	f.infosErr = errors.New("mongo down")
	errs := handleAvatars(t, f, added(t, roomA), removedFrom(t, roomB))
	if errs[0] == nil || errs[1] == nil {
		t.Fatalf("errs = %v", errs)
	}
}

func TestABatchOverTheRoomReadCapIsReadInChunks(t *testing.T) {
	f := newFakeAvatars()
	evs := make([]projection.Event, store.RoomActivityBatch+1)
	for i := range evs {
		room := roomA + uint64(i)
		f.group(room, 0)
		f.samples[room] = []string{fmt.Sprintf("u%d", i)}
		evs[i] = added(t, room)
	}
	allNil(t, handleAvatars(t, f, evs...))
	if len(f.infos) != 2 || len(f.infos[0]) != store.RoomActivityBatch || len(f.infos[1]) != 1 || len(f.sets) != len(evs) {
		t.Fatalf("read %d chunks, wrote %d rooms", len(f.infos), len(f.sets))
	}
}

func TestManyAddsToOneRoomReadItOnce(t *testing.T) {
	f := newFakeAvatars()
	f.group(roomA, 0)
	f.samples[roomA] = []string{"a", "b", "c", "d"}
	evs := make([]projection.Event, 500)
	for i := range evs {
		evs[i] = added(t, roomA)
	}
	allNil(t, handleAvatars(t, f, evs...))
	if len(f.infos) != 1 || len(f.infos[0]) != 1 || len(f.sampled) != 1 || len(f.sets) != 1 {
		t.Fatalf("infos %d, sampled %d, sets %d", len(f.infos), len(f.sampled), len(f.sets))
	}
}
