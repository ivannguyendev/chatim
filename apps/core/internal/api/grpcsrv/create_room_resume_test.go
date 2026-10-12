package grpcsrv_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type recordingRegistry struct {
	acceptAllCIDs
	mu      sync.Mutex
	commits []dedupe.Entry
	aborts  int
}

func (r *recordingRegistry) Commit(_ context.Context, entries []dedupe.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits = append(r.commits, entries...)
	return nil
}

func (r *recordingRegistry) Abort(context.Context, []dedupe.Key) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aborts++
	return nil
}

func (r *recordingRegistry) seen() ([]dedupe.Entry, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dedupe.Entry(nil), r.commits...), r.aborts
}

type joinsAfterFinish struct {
	*memstore.Rooms
	reg       *recordingRegistry
	mu        sync.Mutex
	finished  []bool
	failFirst bool
}

func (j *joinsAfterFinish) AddMembers(ctx context.Context, jn domain.Join, users []string) (store.JoinResult, error) {
	commits, _ := j.reg.seen()
	j.mu.Lock()
	j.finished = append(j.finished, len(commits) == 1 && commits[0].Record.Seq == jn.Room)
	fail := j.failFirst && len(j.finished) == 1
	j.mu.Unlock()
	if fail {
		return store.JoinResult{}, errors.New("core died after finishing the request")
	}
	return j.Rooms.AddMembers(ctx, jn, users)
}

var teamRequest = &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice", "bob"}, RequestId: "r1"}

func expectFoundedTeam(t *testing.T, rg *rig, room uint64) {
	t.Helper()
	stored, err := rg.rooms.Get(t.Context(), room)
	if err != nil || stored.MemberCount != 2 || stored.MemberCountVer != 1 {
		t.Fatalf("room %d = %+v, %v; want 2 members at count ver 1", room, stored, err)
	}
	for user, role := range map[string]domain.Role{"alice": domain.RoleOwner, "bob": domain.RoleMember} {
		if m, err := rg.rooms.Member(t.Context(), room, user); err != nil || m.Role != role {
			t.Fatalf("member %s = %+v, %v; want role %s", user, m, err, role)
		}
	}
}

func TestCreateRoomRetryFinishesTheRoomItsRequestInserted(t *testing.T) {
	newID, _ := idSequence(42, 43)
	reg := &recordingRegistry{}
	var spy *joinsAfterFinish
	wrap := func(rooms *memstore.Rooms) grpcsrv.RoomMembers {
		spy = &joinsAfterFinish{Rooms: rooms, reg: reg, failFirst: true}
		return spy
	}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, members: wrap, pending: reg})
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), teamRequest)
	expectCode(t, err, codes.Internal)
	if half, err := rg.rooms.Get(t.Context(), 42); err != nil || half.MemberCount != 0 {
		t.Fatalf("room 42 after the failed members = %+v, %v; want stored without members", half, err)
	}
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), teamRequest)
	if err != nil || resp.GetRoom().GetId() != "42" || resp.GetRoom().GetMemberCount() != 2 {
		t.Fatalf("retry = %v, %v; want room 42 finished with 2 members", resp.GetRoom(), err)
	}
	expectFoundedTeam(t, rg, 42)
	if _, err := rg.rooms.Get(t.Context(), 43); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("room 43 = %v, want no second room", err)
	}
	if commits, aborts := reg.seen(); len(commits) != 1 || commits[0].Record.Seq != 42 || aborts != 0 {
		t.Fatalf("dedupe commits %+v and %d aborts, want one commit of room 42 and no abort", commits, aborts)
	}
	if again, err := rg.client.CreateRoom(as(t, "acme", "alice"), teamRequest); err != nil || again.GetRoom().GetMemberCount() != 2 {
		t.Fatalf("third call = %v, %v; want room 42 of 2", again.GetRoom(), err)
	}
	if len(spy.finished) != 2 || !spy.finished[0] || !spy.finished[1] {
		t.Fatalf("members written with the request finished = %v, want two writes after the finish", spy.finished)
	}
}

func TestCreateRoomRetryNeverRevivesARemovedFounder(t *testing.T) {
	newID, _ := idSequence(42, 43)
	rg := newRig(t, options{newID: newID})
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), teamRequest); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if _, err := rg.client.RemoveMember(as(t, "acme", "alice"), &chatimv1.RemoveMemberRequest{RoomId: "42", User: "bob"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), teamRequest)
	if err != nil || resp.GetRoom().GetId() != "42" || resp.GetRoom().GetMemberCount() != 1 {
		t.Fatalf("retry = %v, %v; want room 42 of 1", resp.GetRoom(), err)
	}
	if _, err := rg.rooms.Member(t.Context(), 42, "bob"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("bob after the retry = %v, want still removed", err)
	}
}
