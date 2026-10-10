package grpcsrv_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type failingJoins struct {
	*memstore.Rooms
	fails atomic.Int32
}

func (f *failingJoins) AddMembers(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	if f.fails.Add(-1) >= 0 {
		return store.JoinResult{}, errors.New("core died before the members")
	}
	return f.Rooms.AddMembers(ctx, j, users)
}

func openDirect(t *testing.T, rg *rig, user, other string) *chatimv1.OpenDirectRoomResponse {
	t.Helper()
	resp, err := rg.client.OpenDirectRoom(as(t, "acme", user), &chatimv1.OpenDirectRoomRequest{OtherUser: other})
	if err != nil {
		t.Fatalf("OpenDirectRoom(%s, %s): %v", user, other, err)
	}
	return resp
}

func expectDirectPair(t *testing.T, rg *rig, room uint64) {
	t.Helper()
	stored, err := rg.rooms.Get(t.Context(), room)
	if err != nil || stored.Type != domain.RoomDM || stored.MemberCount != 2 || stored.MemberCountVer != 1 || stored.DMKey != domain.DirectKey("acme", "alice", "bob") {
		t.Fatalf("room %d = %+v, %v; want a dm of alice and bob with 2 members at count ver 1", room, stored, err)
	}
	for _, u := range []string{"alice", "bob"} {
		if m, err := rg.rooms.Member(t.Context(), room, u); err != nil || m.Role != domain.RoleMember || m.Ver != 1 {
			t.Fatalf("member %s of room %d = %+v, %v; want role member at ver 1", u, room, m, err)
		}
	}
}

func TestOpenDirectRoomTwiceGivesOneRoom(t *testing.T) {
	newID, _ := idSequence(8812, 8813)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, events: events})
	first := openDirect(t, rg, "alice", "bob")
	if !first.GetCreated() || first.GetRoom().GetId() != "8812" || first.GetRoom().GetMemberCount() != 2 || first.GetRoom().GetType() != chatimv1.RoomType_ROOM_TYPE_DM {
		t.Fatalf("first open = %v, want a new dm 8812 of 2", first)
	}
	again := openDirect(t, rg, "bob", "alice")
	if again.GetCreated() || again.GetRoom().GetId() != "8812" || again.GetRoom().GetMemberCount() != 2 {
		t.Fatalf("second open = %v, want dm 8812 not created", again)
	}
	expectDirectPair(t, rg, 8812)
	if _, ids := events.enqueued(); !slices.Equal(eventIDs(ids), createdIDs(8812, "alice", "bob")) {
		t.Fatalf("enqueued %v, want %v once", eventIDs(ids), createdIDs(8812, "alice", "bob"))
	}
	if _, err := rg.rooms.Get(t.Context(), 8813); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("room 8813 = %v, want ErrRoomNotFound", err)
	}
}

func TestOpenDirectRoomRefusesSelfAndBadUsers(t *testing.T) {
	rg := newRig(t, options{sender: &fakeSender{}})
	for _, other := range []string{"alice", "", "b.b"} {
		_, err := rg.client.OpenDirectRoom(as(t, "acme", "alice"), &chatimv1.OpenDirectRoomRequest{OtherUser: other})
		expectCode(t, err, codes.InvalidArgument)
	}
}

func TestOpenDirectRoomAsksThePolicy(t *testing.T) {
	newID, _ := idSequence(5)
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		if r.Action == access.OpenDirect && r.User == "alice" {
			return access.ErrDenied
		}
		return nil
	})
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, policy: deny})
	_, err := rg.client.OpenDirectRoom(as(t, "acme", "alice"), &chatimv1.OpenDirectRoomRequest{OtherUser: "bob"})
	expectCode(t, err, codes.PermissionDenied)
	if got := openDirect(t, rg, "bob", "alice"); !got.GetCreated() {
		t.Fatalf("open by bob = %v, want created after alice was refused", got)
	}
}

func TestOpenDirectRoomFinishesAnInterruptedOpen(t *testing.T) {
	newID, _ := idSequence(8812, 8813)
	wrap := func(rooms *memstore.Rooms) grpcsrv.RoomMembers {
		spy := &failingJoins{Rooms: rooms}
		spy.fails.Store(1)
		return spy
	}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, members: wrap})
	rooms := rg.rooms
	_, err := rg.client.OpenDirectRoom(as(t, "acme", "alice"), &chatimv1.OpenDirectRoomRequest{OtherUser: "bob"})
	expectCode(t, err, codes.Internal)
	if half, err := rooms.Get(t.Context(), 8812); err != nil || half.MemberCount != 0 {
		t.Fatalf("room after the interrupted open = %+v, %v; want stored with no members", half, err)
	}
	if got := openDirect(t, rg, "bob", "alice"); !got.GetCreated() || got.GetRoom().GetId() != "8812" || got.GetRoom().GetMemberCount() != 2 {
		t.Fatalf("open after the interruption = %v, want dm 8812 finished with 2 members", got)
	}
	expectDirectPair(t, rg, 8812)
	if got := openDirect(t, rg, "alice", "bob"); got.GetCreated() {
		t.Fatalf("third open = %v, want not created", got)
	}
}

func TestOpenDirectRoomMovesOffARoomIDOfAnotherRoom(t *testing.T) {
	newID, _ := idSequence(5, 6)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID})
	occupy(t, rg.rooms, 5)
	_, err := rg.client.OpenDirectRoom(as(t, "acme", "alice"), &chatimv1.OpenDirectRoomRequest{OtherUser: "bob"})
	expectCode(t, err, codes.Unavailable)
	if taken, err := rg.rooms.Get(t.Context(), 5); err != nil || taken.Type != domain.RoomGroup || taken.MemberCount != 1 {
		t.Fatalf("room 5 = %+v, %v; want the other group kept", taken, err)
	}
	if got := openDirect(t, rg, "bob", "alice"); !got.GetCreated() || got.GetRoom().GetId() != "6" {
		t.Fatalf("open after the move = %v, want dm 6 created", got)
	}
	expectDirectPair(t, rg, 6)
}

func TestOpenDirectRoomInParallelGivesOneRoom(t *testing.T) {
	rg := newRig(t, options{sender: &fakeSender{}})
	const openers = 8
	resps := make([]*chatimv1.OpenDirectRoomResponse, openers)
	errs := make([]error, openers)
	var wg sync.WaitGroup
	for i := range openers {
		wg.Go(func() {
			user, other := "alice", "bob"
			if i%2 == 1 {
				user, other = other, user
			}
			resps[i], errs[i] = rg.client.OpenDirectRoom(as(t, "acme", user), &chatimv1.OpenDirectRoomRequest{OtherUser: other})
		})
	}
	wg.Wait()
	created := 0
	for i := range openers {
		if errs[i] != nil || resps[i].GetRoom().GetId() != resps[0].GetRoom().GetId() {
			t.Fatalf("opener %d = %v, %v; want the room %s of opener 0", i, resps[i], errs[i], resps[0].GetRoom().GetId())
		}
		if resps[i].GetCreated() {
			created++
		}
	}
	if created == 0 {
		t.Fatal("no opener reported the room as created")
	}
	expectDirectPair(t, rg, roomNumber(t, resps[0].GetRoom().GetId()))
}
