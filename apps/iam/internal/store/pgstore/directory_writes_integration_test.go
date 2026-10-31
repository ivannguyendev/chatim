package pgstore_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func group(id, parent string) store.Group {
	return store.Group{
		TenantID: "shop", ID: id, Name: "Group " + id, ParentID: parent, Mentionable: true,
		State: store.GroupLive, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "test",
	}
}

func createGroups(t *testing.T, s fixture, groups ...store.Group) {
	t.Helper()
	for _, g := range groups {
		if err := s.CreateGroup(context.Background(), g, audit("group_create")); err != nil {
			t.Fatalf("CreateGroup %s: %v", g.ID, err)
		}
	}
}

func moveUnder(parent string) store.GroupUpdate { return store.GroupUpdate{ParentID: &parent} }

func wantErr(t *testing.T, err, target error, step string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v; want %v", step, err, target)
	}
}

func readGroup(t *testing.T, s fixture, id string) store.Group {
	t.Helper()
	g, err := s.Group(context.Background(), "shop", id)
	if err != nil {
		t.Fatalf("Group %s: %v", id, err)
	}
	g.CreatedAt, g.UpdatedAt = g.CreatedAt.UTC(), g.UpdatedAt.UTC()
	return g
}

func groupsOf(t *testing.T, s fixture, account string) []string {
	t.Helper()
	rows, err := s.raw.Query(context.Background(), "SELECT group_id FROM account_groups WHERE tenant_id = 'shop' AND account_id = $1 ORDER BY group_id", account)
	if err != nil {
		t.Fatalf("query account_groups: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect account_groups: %v", err)
	}
	return ids
}

func wantGroupsOf(t *testing.T, s fixture, account string, want []string, step string) {
	t.Helper()
	if got := groupsOf(t, s, account); !slices.Equal(got, want) {
		t.Fatalf("%s: groups of %s = %v; want %v", step, account, got, want)
	}
}

func wantAudits(t *testing.T, s fixture, action string, want int) {
	t.Helper()
	var got int
	if err := s.raw.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE tenant_id = 'shop' AND action = $1", action).Scan(&got); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	if got != want {
		t.Fatalf("audit rows %s = %d; want %d", action, got, want)
	}
}

func TestCreateGroupNeedsALiveParent(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("sales", ""), group("sales-hn", "sales"), group("old", ""))
	got := readGroup(t, s, "sales-hn")
	want := group("sales-hn", "sales")
	if got != want {
		t.Fatalf("Group = %+v; want %+v", got, want)
	}
	wantErr(t, s.CreateGroup(ctx, group("orphan", "missing"), audit("group_create")), apperr.ErrNotFound, "missing parent")
	if err := s.DeleteGroup(ctx, "shop", "old", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	wantErr(t, s.CreateGroup(ctx, group("late", "old"), audit("group_create")), apperr.ErrNotFound, "deleted parent")
	wantErr(t, s.CreateGroup(ctx, group("sales", ""), audit("group_create")), apperr.ErrAlreadyExists, "duplicate id")
	wantErr(t, s.CreateGroup(ctx, group("old", ""), audit("group_create")), apperr.ErrAlreadyExists, "id of a deleted group")
	_, err := s.Group(ctx, "shop", "missing")
	wantErr(t, err, apperr.ErrNotFound, "read missing")
	wantAudits(t, s, "group_create", 3)
}

func TestUpdateGroupChangesOnlyTheGivenFields(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("sales", ""), group("support", ""), group("sales-hn", "sales"), group("old", ""))
	at := audit("group_update")
	at.At = t0.Add(5 * time.Minute)
	at.Actor = "admin-cli"
	if err := s.UpdateGroup(ctx, "shop", "sales-hn", store.GroupUpdate{Name: new("Sales Ha Noi")}, at); err != nil {
		t.Fatalf("rename: %v", err)
	}
	want := group("sales-hn", "sales")
	want.Name, want.UpdatedAt, want.UpdatedBy = "Sales Ha Noi", at.At, "admin-cli"
	if got := readGroup(t, s, "sales-hn"); got != want {
		t.Fatalf("after rename = %+v; want %+v", got, want)
	}
	if err := s.UpdateGroup(ctx, "shop", "sales-hn", store.GroupUpdate{Mentionable: new(false)}, at); err != nil {
		t.Fatalf("mentionable: %v", err)
	}
	if err := s.UpdateGroup(ctx, "shop", "sales-hn", moveUnder("support"), at); err != nil {
		t.Fatalf("move: %v", err)
	}
	want.Mentionable, want.ParentID = false, "support"
	if got := readGroup(t, s, "sales-hn"); got != want {
		t.Fatalf("after move = %+v; want %+v", got, want)
	}
	if err := s.UpdateGroup(ctx, "shop", "sales-hn", moveUnder(""), at); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	want.ParentID = ""
	if got := readGroup(t, s, "sales-hn"); got != want {
		t.Fatalf("after move to root = %+v; want %+v", got, want)
	}
	if err := s.DeleteGroup(ctx, "shop", "old", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	wantErr(t, s.UpdateGroup(ctx, "shop", "sales-hn", moveUnder("old"), at), apperr.ErrNotFound, "deleted parent")
	wantErr(t, s.UpdateGroup(ctx, "shop", "sales-hn", moveUnder("missing"), at), apperr.ErrNotFound, "missing parent")
	wantErr(t, s.UpdateGroup(ctx, "shop", "old", store.GroupUpdate{Name: new("x")}, at), apperr.ErrNotFound, "deleted group")
	wantErr(t, s.UpdateGroup(ctx, "shop", "missing", store.GroupUpdate{Name: new("x")}, at), apperr.ErrNotFound, "missing group")
	wantAudits(t, s, "group_update", 4)
}

func TestMovingAGroupUnderItsDescendantIsRefused(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("a", ""), group("b", "a"), group("c", "b"))
	for _, target := range []string{"a", "b", "c"} {
		err := s.UpdateGroup(ctx, "shop", "a", moveUnder(target), audit("group_update"))
		wantErr(t, err, apperr.ErrFailedPrecondition, "move a under "+target)
	}
	for id, parent := range map[string]string{"a": "", "b": "a", "c": "b"} {
		if got := readGroup(t, s, id).ParentID; got != parent {
			t.Fatalf("parent of %s = %q; want %q", id, got, parent)
		}
	}
	wantAudits(t, s, "group_update", 0)
	if err := s.UpdateGroup(ctx, "shop", "c", moveUnder("a"), audit("group_update")); err != nil {
		t.Fatalf("move c under its grandparent: %v", err)
	}
	wantAudits(t, s, "group_update", 1)
}

func TestDeletingAGroupWithChildrenIsRefused(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("sales", ""), group("sales-hn", "sales"))
	wantErr(t, s.DeleteGroup(ctx, "shop", "sales", audit("group_delete")), apperr.ErrFailedPrecondition, "live child")
	if got := readGroup(t, s, "sales").State; got != store.GroupLive {
		t.Fatalf("state after refused delete = %d; want live", got)
	}
	wantAudits(t, s, "group_delete", 0)
	if err := s.DeleteGroup(ctx, "shop", "sales-hn", audit("group_delete")); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if err := s.DeleteGroup(ctx, "shop", "sales", audit("group_delete")); err != nil {
		t.Fatalf("delete parent after child: %v", err)
	}
	if got := readGroup(t, s, "sales").State; got != store.GroupDeleted {
		t.Fatalf("state = %d; want deleted", got)
	}
	wantErr(t, s.DeleteGroup(ctx, "shop", "sales", audit("group_delete")), apperr.ErrNotFound, "delete again")
	wantErr(t, s.DeleteGroup(ctx, "shop", "missing", audit("group_delete")), apperr.ErrNotFound, "missing")
	wantAudits(t, s, "group_delete", 2)
}

func TestDeletingAGroupRemovesMembershipsAndBumpsEachUser(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	for id, username := range map[string]string{lanID: "lan", minhID: "minh", anID: "an"} {
		seedAccount(t, s, "shop", id, username)
	}
	createGroups(t, s, group("sales", ""), group("support", ""))
	for account, groups := range map[string][]string{lanID: {"sales", "support"}, minhID: {"sales"}, anID: {"support"}} {
		if err := s.SetAccountGroups(ctx, "shop", account, groups, audit("account_groups_set")); err != nil {
			t.Fatalf("SetAccountGroups: %v", err)
		}
	}
	for _, id := range []string{lanID, minhID, anID} {
		wantVer(t, s, userKind, id, int64(1+len(groupsOf(t, s, id))), "before delete")
	}
	wantVer(t, s, groupKind, "sales", 1, "before delete")
	if err := s.DeleteGroup(ctx, "shop", "sales", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	wantGroupsOf(t, s, lanID, []string{"support"}, "lan")
	wantGroupsOf(t, s, minhID, nil, "minh")
	wantGroupsOf(t, s, anID, []string{"support"}, "an")
	wantVer(t, s, userKind, lanID, 4, "lan after delete")
	wantVer(t, s, userKind, minhID, 3, "minh after delete")
	wantVer(t, s, userKind, anID, 2, "an after delete")
	wantVer(t, s, groupKind, "sales", 2, "group after delete")
	wantAudits(t, s, "group_delete", 1)
}

func TestSetAccountGroupsReplacesTheWholeList(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", lanID, "lan")
	createGroups(t, s, group("a", ""), group("b", ""), group("c", ""), group("old", ""))
	if err := s.DeleteGroup(ctx, "shop", "old", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	set := func(ids ...string) error {
		return s.SetAccountGroups(ctx, "shop", lanID, ids, audit("account_groups_set"))
	}
	if err := set("a", "b"); err != nil {
		t.Fatalf("set a b: %v", err)
	}
	wantGroupsOf(t, s, lanID, []string{"a", "b"}, "a b")
	wantVer(t, s, userKind, lanID, 3, "a b")
	if err := set("c", "b"); err != nil {
		t.Fatalf("set c b: %v", err)
	}
	wantGroupsOf(t, s, lanID, []string{"b", "c"}, "b c")
	wantVer(t, s, userKind, lanID, 5, "b c")
	if err := set("b", "c"); err != nil {
		t.Fatalf("same set: %v", err)
	}
	wantVer(t, s, userKind, lanID, 5, "same set")
	wantErr(t, set("b", "missing"), apperr.ErrNotFound, "missing group")
	wantErr(t, set("b", "old"), apperr.ErrNotFound, "deleted group")
	wantErr(t, set("b", "b"), apperr.ErrInvalidArgument, "duplicate")
	wantErr(t, s.SetAccountGroups(ctx, "shop", minhID, []string{"a"}, audit("account_groups_set")), apperr.ErrNotFound, "missing account")
	wantGroupsOf(t, s, lanID, []string{"b", "c"}, "after refusals")
	if err := set(); err != nil {
		t.Fatalf("set none: %v", err)
	}
	wantGroupsOf(t, s, lanID, nil, "none")
	wantVer(t, s, userKind, lanID, 7, "none")
	wantAudits(t, s, "account_groups_set", 4)
}

func TestSetProfileChangesOnlyTheGivenFields(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", lanID, "lan")
	at := audit("profile_set")
	at.At = t0.Add(5 * time.Minute)
	at.Actor = "admin-cli"
	profile := func() (name, avatar, by string) {
		t.Helper()
		if err := s.raw.QueryRow(ctx, "SELECT display_name, avatar_url, updated_by FROM accounts WHERE id = $1", lanID).Scan(&name, &avatar, &by); err != nil {
			t.Fatalf("read profile: %v", err)
		}
		return name, avatar, by
	}
	if err := s.SetProfile(ctx, "shop", lanID, store.ProfileUpdate{DisplayName: new("Lan Nguyen")}, at); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if name, avatar, by := profile(); name != "Lan Nguyen" || avatar != "" || by != "admin-cli" {
		t.Fatalf("after name = %q %q %q", name, avatar, by)
	}
	if err := s.SetProfile(ctx, "shop", lanID, store.ProfileUpdate{AvatarURL: new("https://cdn.example/lan.png")}, at); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	if name, avatar, _ := profile(); name != "Lan Nguyen" || avatar != "https://cdn.example/lan.png" {
		t.Fatalf("after avatar = %q %q", name, avatar)
	}
	wantVer(t, s, userKind, lanID, 3, "two changes")
	if err := s.SetProfile(ctx, "shop", lanID, store.ProfileUpdate{AvatarURL: new("")}, at); err != nil {
		t.Fatalf("clear avatar: %v", err)
	}
	if _, avatar, _ := profile(); avatar != "" {
		t.Fatalf("avatar after clear = %q", avatar)
	}
	a, err := s.AccountByID(ctx, "shop", lanID)
	if err != nil || a.Ver != 1 {
		t.Fatalf("account ver = %d, %v; want 1 (profile never moves the login CAS)", a.Ver, err)
	}
	wantErr(t, s.SetProfile(ctx, "shop", minhID, store.ProfileUpdate{DisplayName: new("Minh")}, at), apperr.ErrNotFound, "missing account")
	wantAudits(t, s, "profile_set", 3)
}
