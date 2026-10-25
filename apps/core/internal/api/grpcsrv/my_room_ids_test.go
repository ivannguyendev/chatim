package grpcsrv_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type brokenRoomVersions struct{ *memstore.Rooms }

func (brokenRoomVersions) RoomVersionsOf(context.Context, string, string) ([]store.RoomVersion, error) {
	return nil, errors.New("server selection timeout")
}

func myRoomIDs(t *testing.T, rg *rig, tenant, user string) (rooms, left []string) {
	t.Helper()
	resp, err := rg.client.ListMyRoomIDs(as(t, tenant, user), &chatimv1.ListMyRoomIDsRequest{})
	if err != nil {
		t.Fatalf("ListMyRoomIDs(%s/%s): %v", tenant, user, err)
	}
	return roomVersionLabels(resp.GetRooms()), roomVersionLabels(resp.GetLeft())
}

func roomVersionLabels(rooms []*chatimv1.MyRoomID) []string {
	out := make([]string, len(rooms))
	for i, r := range rooms {
		out[i] = r.GetRoomId() + "@v" + strconv.FormatUint(uint64(r.GetVer()), 10)
	}
	slices.SortFunc(out, func(a, b string) int { return cmp.Compare(roomLabelNumber(a), roomLabelNumber(b)) })
	return out
}

func roomLabelNumber(label string) uint64 {
	id, _, _ := strings.Cut(label, "@")
	n, _ := strconv.ParseUint(id, 10, 64)
	return n
}

func TestListMyRoomIDsListsTheCallersActiveAndLeftRoomsWithTheirVersions(t *testing.T) {
	rg := newRig(t, options{})
	first := rg.createGroup(t, "acme", "alice", "bob")
	second := rg.createGroup(t, "acme", "carol", "bob")
	left := rg.createGroup(t, "acme", "alice", "bob")
	rg.createGroup(t, "globex", "alice", "bob")
	if _, err := rg.client.LeaveRoom(as(t, "acme", "bob"), &chatimv1.LeaveRoomRequest{RoomId: left}); err != nil {
		t.Fatalf("LeaveRoom: %v", err)
	}
	if _, err := rg.client.ChangeMemberRole(as(t, "acme", "alice"), &chatimv1.ChangeMemberRoleRequest{RoomId: first, User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}); err != nil {
		t.Fatalf("ChangeMemberRole: %v", err)
	}
	want := roomVersionLabels([]*chatimv1.MyRoomID{{RoomId: first, Ver: 2}, {RoomId: second, Ver: 1}})
	rooms, gone := myRoomIDs(t, rg, "acme", "bob")
	if !slices.Equal(rooms, want) || !slices.Equal(gone, []string{left + "@v2"}) {
		t.Fatalf("bob's rooms = %v, left = %v; want %v and [%s@v2]", rooms, gone, want, left)
	}
	if rooms, gone := myRoomIDs(t, rg, "acme", "dave"); len(rooms) != 0 || len(gone) != 0 {
		t.Fatalf("dave's rooms = %v, left = %v; want none", rooms, gone)
	}
}

func TestListMyRoomIDsReportsAStoreFailureAsUnavailable(t *testing.T) {
	rg := newRig(t, options{memberReads: func(r *memstore.Rooms) member.MemberLister { return brokenRoomVersions{r} }})
	_, err := rg.client.ListMyRoomIDs(as(t, "acme", "bob"), &chatimv1.ListMyRoomIDsRequest{})
	expectCode(t, err, codes.Unavailable)
}
