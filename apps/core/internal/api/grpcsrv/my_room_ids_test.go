package grpcsrv_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
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

func myRoomIDs(t *testing.T, rg *rig, tenant, user string) []string {
	t.Helper()
	resp, err := rg.client.ListMyRoomIDs(as(t, tenant, user), &chatimv1.ListMyRoomIDsRequest{})
	if err != nil {
		t.Fatalf("ListMyRoomIDs(%s/%s): %v", tenant, user, err)
	}
	out := make([]string, len(resp.GetRooms()))
	for i, r := range resp.GetRooms() {
		out[i] = r.GetRoomId() + "@v" + strconv.FormatUint(uint64(r.GetVer()), 10)
	}
	return out
}

func byRoomNumber(rooms ...string) []string {
	slices.SortFunc(rooms, func(a, b string) int {
		x, _ := strconv.ParseUint(a, 10, 64)
		y, _ := strconv.ParseUint(b, 10, 64)
		return cmp.Compare(x, y)
	})
	return rooms
}

func TestListMyRoomIDsListsTheCallersActiveRoomsWithTheirVersions(t *testing.T) {
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
	rooms := byRoomNumber(first, second)
	want := make([]string, len(rooms))
	for i, r := range rooms {
		want[i] = r + "@v1"
		if r == first {
			want[i] = r + "@v2"
		}
	}
	if got := myRoomIDs(t, rg, "acme", "bob"); !slices.Equal(got, want) {
		t.Fatalf("bob's rooms = %v, want %v", got, want)
	}
	if got := myRoomIDs(t, rg, "acme", "dave"); len(got) != 0 {
		t.Fatalf("dave's rooms = %v, want none", got)
	}
}

func TestListMyRoomIDsReportsAStoreFailureAsUnavailable(t *testing.T) {
	rg := newRig(t, options{memberReads: func(r *memstore.Rooms) member.MemberLister { return brokenRoomVersions{r} }})
	_, err := rg.client.ListMyRoomIDs(as(t, "acme", "bob"), &chatimv1.ListMyRoomIDsRequest{})
	expectCode(t, err, codes.Unavailable)
}
