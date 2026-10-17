package grpcsrv_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func groupRig(t *testing.T) *mentionRig {
	t.Helper()
	rg := newMentionRig(t, options{})
	rg.catalog(t,
		domain.Group{ID: "sale", Name: "Ban hang", Active: true, Ver: 1},
		domain.Group{ID: "retired", Name: "Retired", Active: false, Ver: 1},
		domain.Group{ID: "hcm", Name: "HCM", Active: true, Ver: 1})
	rg.memberOf(t, "alice", "ops", "sale")
	rg.memberOf(t, "bob", "hanoi", "retired")
	rg.memberOf(t, "carol", "hcm")
	return rg
}

func (rg *mentionRig) memberOf(t *testing.T, user string, groups ...string) {
	t.Helper()
	of, err := rg.userGroups.Of(t.Context(), "acme", []string{user})
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	ver := uint64(len(of[user]) + 1)
	if _, err := rg.userGroups.Set(t.Context(), []domain.UserGroups{{Tenant: "acme", User: user, Groups: groups, Ver: ver * 10}}); err != nil {
		t.Fatalf("UserGroups.Set: %v", err)
	}
}

func roomGroups(t *testing.T, rg *mentionRig, room uint64) ([]string, bool) {
	t.Helper()
	resp, err := rg.svc.ListRoomGroups(rg.bob, &chatimv1.ListRoomGroupsRequest{RoomId: ids.FormatRoomID(room)})
	if err != nil {
		t.Fatalf("ListRoomGroups(%d): %v", room, err)
	}
	var out []string
	for _, g := range resp.GetGroups() {
		out = append(out, g.GetId()+"="+g.GetName())
	}
	return out, resp.GetFiltered()
}

func TestListRoomGroupsChecksTheCallerFirst(t *testing.T) {
	rg := groupRig(t)
	theirs := rg.room(t, 0, "carol")
	for name, tc := range map[string]struct {
		room string
		want codes.Code
	}{
		"not a member": {ids.FormatRoomID(theirs), codes.PermissionDenied},
		"no room":      {"999999", codes.NotFound},
		"bad room id":  {"x", codes.InvalidArgument},
	} {
		_, err := rg.svc.ListRoomGroups(rg.bob, &chatimv1.ListRoomGroupsRequest{RoomId: tc.room})
		expectCode(t, name, err, tc.want)
	}
	_, err := rg.svc.ListRoomGroups(t.Context(), &chatimv1.ListRoomGroupsRequest{RoomId: ids.FormatRoomID(theirs)})
	expectCode(t, "no caller", err, codes.Unauthenticated)
	mine := rg.room(t, 0, "bob")
	rg.core.fail(errDown)
	_, err = rg.svc.ListRoomGroups(rg.bob, &chatimv1.ListRoomGroupsRequest{RoomId: ids.FormatRoomID(mine)})
	expectCode(t, "core down", err, codes.Unavailable)
}

func TestListRoomGroupsOfASmallRoomListsTaggableGroupsOfItsMembersByName(t *testing.T) {
	rg := groupRig(t)
	room := rg.room(t, 0, "bob")
	got, filtered := roomGroups(t, rg, room)
	if want := []string{"sale=Ban hang", "hanoi=Hanoi", "ops=Ops"}; !slices.Equal(got, want) || !filtered {
		t.Fatalf("groups = %v filtered %v, want %v filtered", got, filtered, want)
	}
	rg.join(t, room, 5, "carol")
	if got, _ := roomGroups(t, rg, room); slices.Contains(got, "hcm=HCM") {
		t.Fatalf("groups = %v before the member event, want the cached set", got)
	}
	rg.sets.Forget(room)
	if got, _ := roomGroups(t, rg, room); !slices.Contains(got, "hcm=HCM") {
		t.Fatalf("groups = %v after the member event, want carol's hcm", got)
	}
}

func TestListRoomGroupsOfABigRoomIsUnfilteredUntilItsBuildLands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := groupRig(t)
		room := rg.room(t, 0, "bob", "dave", "erin")
		got, filtered := roomGroups(t, rg, room)
		if want := []string{"sale=Ban hang", "hcm=HCM", "hanoi=Hanoi", "ops=Ops"}; !slices.Equal(got, want) || filtered {
			t.Fatalf("first call = %v filtered %v, want the whole catalog unfiltered", got, filtered)
		}
		synctest.Wait()
		got, filtered = roomGroups(t, rg, room)
		if want := []string{"sale=Ban hang", "hanoi=Hanoi", "ops=Ops"}; !slices.Equal(got, want) || !filtered {
			t.Fatalf("after the build = %v filtered %v, want %v filtered", got, filtered, want)
		}
	})
}
