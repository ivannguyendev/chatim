package grpcsrv_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type failingUserGroups struct{ *memstore.UserGroups }

func (failingUserGroups) Set(context.Context, []domain.UserGroups) ([]string, error) {
	return nil, errDown
}

type failingGroups struct{ *memstore.Groups }

func (g failingGroups) Set(ctx context.Context, items []domain.Group) ([]string, error) {
	if _, err := g.Groups.Set(ctx, items); err != nil {
		return nil, err
	}
	return nil, errDown
}

func TestSetUserGroupsWritesForTheCallerTenant(t *testing.T) {
	rg := newMentionRig(t, options{})
	req := &chatimv1.SetUserGroupsRequest{Items: []*chatimv1.UserGroupsItem{
		{User: "bob", Groups: []string{"ops", "hanoi", "ops"}, Ver: 3},
		{User: "carol", Groups: nil, Ver: 1},
	}}
	resp, err := rg.svc.SetUserGroups(rg.alice, req)
	if err != nil || !slices.Equal(resp.GetChangedUsers(), []string{"bob", "carol"}) {
		t.Fatalf("SetUserGroups = %v, %v, want bob and carol changed", resp, err)
	}
	if resp, err = rg.svc.SetUserGroups(rg.alice, req); err != nil || len(resp.GetChangedUsers()) != 0 {
		t.Fatalf("SetUserGroups again = %v, %v, want nothing changed", resp, err)
	}
	of, err := rg.userGroups.Of(t.Context(), "acme", []string{"bob"})
	if err != nil || !slices.Equal(of["bob"], []string{"hanoi", "ops"}) {
		t.Fatalf("stored groups = %v, %v", of, err)
	}
}

func TestSetUserGroupsRejectsBadRequests(t *testing.T) {
	rg := newMentionRig(t, options{})
	big := make([]*chatimv1.UserGroupsItem, 1001)
	for i := range big {
		big[i] = &chatimv1.UserGroupsItem{User: fmt.Sprintf("u%d", i), Ver: 1}
	}
	for name, items := range map[string][]*chatimv1.UserGroupsItem{
		"over 1000":    big,
		"user twice":   {{User: "bob", Ver: 1}, {User: "bob", Ver: 2}},
		"bad user":     {{User: "bad user", Ver: 1}},
		"bad group":    {{User: "bob", Groups: []string{"Bad Id"}, Ver: 1}},
		"version zero": {{User: "bob", Ver: 0}},
	} {
		_, err := rg.svc.SetUserGroups(rg.alice, &chatimv1.SetUserGroupsRequest{Items: items})
		expectCode(t, name, err, codes.InvalidArgument)
	}
	_, err := rg.svc.SetUserGroups(t.Context(), &chatimv1.SetUserGroupsRequest{})
	expectCode(t, "no caller", err, codes.Unauthenticated)
	if resp, err := rg.svc.SetUserGroups(rg.alice, &chatimv1.SetUserGroupsRequest{Items: big[:1000]}); err != nil || len(resp.GetChangedUsers()) != 1000 {
		t.Fatalf("SetUserGroups(1000) = %d changed, %v", len(resp.GetChangedUsers()), err)
	}
}

func TestSetGroupsWritesForgetsTheCatalogAndRejectsBadRequests(t *testing.T) {
	rg := newMentionRig(t, options{})
	before, err := rg.svc.SetGroups(rg.alice, &chatimv1.SetGroupsRequest{Items: []*chatimv1.GroupItem{{Id: "sale", Name: "Sales", Active: true, Ver: 1}}})
	if err != nil || !slices.Equal(before.GetChangedGroups(), []string{"sale"}) {
		t.Fatalf("SetGroups = %v, %v", before, err)
	}
	all, err := rg.groups.All(t.Context(), "acme")
	if i := slices.IndexFunc(all, func(g domain.Group) bool { return g.ID == "sale" }); err != nil || i < 0 || all[i].UpdatedBy != "alice" {
		t.Fatalf("stored groups = %+v, %v, want sale updated by alice", all, err)
	}
	big := make([]*chatimv1.GroupItem, 1001)
	for i := range big {
		big[i] = &chatimv1.GroupItem{Id: fmt.Sprintf("g%d", i), Name: "G", Ver: 1}
	}
	for name, items := range map[string][]*chatimv1.GroupItem{
		"over 1000":    big,
		"group twice":  {{Id: "x", Name: "X", Ver: 1}, {Id: "x", Name: "X", Ver: 2}},
		"bad id":       {{Id: "Bad Id", Name: "X", Ver: 1}},
		"blank name":   {{Id: "x", Name: " ", Ver: 1}},
		"version zero": {{Id: "x", Name: "X"}},
	} {
		_, err := rg.svc.SetGroups(rg.alice, &chatimv1.SetGroupsRequest{Items: items})
		expectCode(t, name, err, codes.InvalidArgument)
	}
}

func TestSetWritesMapStoreFailuresToUnavailable(t *testing.T) {
	rg := newMentionRig(t, options{})
	svc := rg.service(t, func(d *grpcsrv.Deps) {
		d.UserGroups = failingUserGroups{rg.userGroups}
		d.Groups = failingGroups{rg.groups}
	})
	_, err := svc.SetUserGroups(rg.alice, &chatimv1.SetUserGroupsRequest{Items: []*chatimv1.UserGroupsItem{{User: "bob", Ver: 1}}})
	expectCode(t, "user groups store down", err, codes.Unavailable)
	if names := activeNames(t, svc, rg); !slices.Equal(names, []string{"Hanoi", "Ops"}) {
		t.Fatalf("catalog = %v before the failed write", names)
	}
	_, err = svc.SetGroups(rg.alice, &chatimv1.SetGroupsRequest{Items: []*chatimv1.GroupItem{{Id: "ops", Name: "Ops", Ver: 2}}})
	expectCode(t, "groups store down", err, codes.Unavailable)
	if names := activeNames(t, svc, rg); !slices.Equal(names, []string{"Hanoi"}) {
		t.Fatalf("catalog = %v, want it forgotten even when the write reports a failure", names)
	}
	if _, err := grpcsrv.New(grpcsrv.Deps{}); err == nil {
		t.Fatal("New without deps succeeded")
	}
}

func activeNames(t *testing.T, svc *grpcsrv.Service, rg *mentionRig) []string {
	t.Helper()
	room := rg.room(t, 0, "bob", "carol", "dave")
	resp, err := svc.ListRoomGroups(rg.bob, &chatimv1.ListRoomGroupsRequest{RoomId: ids.FormatRoomID(room)})
	if err != nil {
		t.Fatalf("ListRoomGroups: %v", err)
	}
	var out []string
	for _, g := range resp.GetGroups() {
		out = append(out, g.GetName())
	}
	return out
}
