package mongostore

import (
	"errors"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func newGroup(tenantID, id, parent string) store.Group {
	return store.Group{
		TenantID: tenantID, ID: id, Name: "Group " + id, ParentID: parent, Mentionable: true,
		State: store.GroupLive, CreatedAt: itAt, UpdatedAt: itAt, UpdatedBy: "cli:admin",
	}
}

func mustCreateGroup(t *testing.T, s *Store, tenantID, id, parent string) {
	t.Helper()
	if err := s.CreateGroup(t.Context(), newGroup(tenantID, id, parent)); err != nil {
		t.Fatalf("CreateGroup %s/%s under %q: %v", tenantID, id, parent, err)
	}
}

func readGroupDoc(t *testing.T, s *Store, tenantID, id string) groupDoc {
	t.Helper()
	var doc groupDoc
	if err := s.db.Collection(groupsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: tenantKey(tenantID, id)}}).Decode(&doc); err != nil {
		t.Fatalf("read group %s/%s: %v", tenantID, id, err)
	}
	return doc
}

func insertDeletedGroup(t *testing.T, s *Store, tenantID, id string) {
	t.Helper()
	insertDoc(t, s, groupsCollection, bson.D{
		{Key: "_id", Value: tenantKey(tenantID, id)}, {Key: "tenant_id", Value: tenantID}, {Key: "id", Value: id},
		{Key: "name", Value: "Gone"}, {Key: "parent_id", Value: ""}, {Key: "ancestors", Value: bson.A{}},
		{Key: "mentionable", Value: true}, {Key: "state", Value: store.GroupDeleted}, {Key: "ver", Value: int64(2)},
		{Key: "members_ver", Value: int64(1)}, {Key: "created_at", Value: itAt}, {Key: "updated_at", Value: itAt},
		{Key: "updated_by", Value: "cli:admin"},
	})
}

func countGroups(t *testing.T, s *Store) int64 {
	t.Helper()
	n, err := s.db.Collection(groupsCollection).CountDocuments(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("count groups: %v", err)
	}
	return n
}

func TestCreateGroupStoresARootAndAChildUnderItsParent(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "sales", "")
	mustCreateGroup(t, s, "shop", "north", "sales")
	root := readGroupDoc(t, s, "shop", "sales")
	if root.TenantID != "shop" || root.ID != "sales" || root.Name != "Group sales" || root.ParentID != "" || len(root.Ancestors) != 0 ||
		!root.Mentionable || root.State != store.GroupLive || root.Ver != 1 || root.MembersVer != 0 ||
		!root.CreatedAt.Equal(itAt) || !root.UpdatedAt.Equal(itAt) || root.UpdatedBy != "cli:admin" {
		t.Fatalf("root group = %+v; want a live root, ver 1, members_ver 0", root)
	}
	child := readGroupDoc(t, s, "shop", "north")
	if child.ParentID != "sales" || !slices.Equal(child.Ancestors, []string{"sales"}) || child.Ver != 1 {
		t.Fatalf("child group = %+v; want parent sales, ancestors [sales], ver 1", child)
	}
	got, err := s.Group(t.Context(), "shop", "north")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	want := newGroup("shop", "north", "sales")
	if got.TenantID != want.TenantID || got.ID != want.ID || got.Name != want.Name || got.ParentID != want.ParentID ||
		got.Mentionable != want.Mentionable || got.State != want.State || !got.CreatedAt.Equal(itAt) ||
		!got.UpdatedAt.Equal(itAt) || got.UpdatedBy != want.UpdatedBy {
		t.Fatalf("Group = %+v; want %+v", got, want)
	}
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 2 || tenant.Ver != 1 {
		t.Fatalf("tenant = %+v; want group_tree_ver 2 and ver 1 kept", tenant)
	}
}

func TestCreateGroupWithAnUnknownOrDeletedParentIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	insertDeletedGroup(t, s, "shop", "gone")
	for _, parent := range []string{"ghost", "gone"} {
		if err := s.CreateGroup(t.Context(), newGroup("shop", "north", parent)); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("CreateGroup under %s = %v; want not found", parent, err)
		}
	}
	if n := countGroups(t, s); n != 1 {
		t.Fatalf("groups = %d; want only the deleted one", n)
	}
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 0 {
		t.Fatalf("group_tree_ver = %d; want 0 after aborted creates", tenant.GroupTreeVer)
	}
}

func TestCreateGroupTwiceIsAlreadyExists(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "sales", "")
	again := newGroup("shop", "sales", "")
	again.Name = "Other"
	if err := s.CreateGroup(t.Context(), again); !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("CreateGroup twice = %v; want already exists", err)
	}
	if got := readGroupDoc(t, s, "shop", "sales"); got.Name != "Group sales" || got.Ver != 1 {
		t.Fatalf("group after the duplicate = %+v; want it unchanged", got)
	}
}

func TestCreateGroupForAnUnknownTenantIsNotFound(t *testing.T) {
	s := testDatabase(t)
	if err := s.CreateGroup(t.Context(), newGroup("ghost", "sales", "")); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("CreateGroup for an unknown tenant = %v; want not found", err)
	}
	if n := countGroups(t, s); n != 0 {
		t.Fatalf("groups = %d; want none", n)
	}
}

func TestUpdateGroupRenameBumpsTheVersionOnlyOnAChange(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "sales", "")
	renameAt := itAt.Add(time.Minute)
	name := "$name"
	if err := s.UpdateGroup(t.Context(), "shop", "sales", store.GroupUpdate{Name: &name}, renameAt, "cli:ops"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed := readGroupDoc(t, s, "shop", "sales")
	if renamed.Name != name || !renamed.Mentionable || renamed.Ver != 2 || !renamed.UpdatedAt.Equal(renameAt) || renamed.UpdatedBy != "cli:ops" {
		t.Fatalf("renamed group = %+v; want name %q stored as text, ver 2, updated at %v by cli:ops", renamed, name, renameAt)
	}
	mentionable := true
	for _, u := range []store.GroupUpdate{{Name: &name}, {Name: &name, Mentionable: &mentionable}, {}} {
		if err := s.UpdateGroup(t.Context(), "shop", "sales", u, renameAt.Add(time.Hour), "cli:other"); err != nil {
			t.Fatalf("UpdateGroup with the same values: %v", err)
		}
		if again := readGroupDoc(t, s, "shop", "sales"); again.Ver != 2 || !again.UpdatedAt.Equal(renameAt) || again.UpdatedBy != "cli:ops" {
			t.Fatalf("group after the same values = %+v; want ver 2 updated at %v by cli:ops", again, renameAt)
		}
	}
	hidden := false
	if err := s.UpdateGroup(t.Context(), "shop", "sales", store.GroupUpdate{Mentionable: &hidden}, renameAt, "cli:ops"); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if got := readGroupDoc(t, s, "shop", "sales"); got.Mentionable || got.Ver != 3 || got.Name != name {
		t.Fatalf("group after hide = %+v; want not mentionable, ver 3, name kept", got)
	}
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 1 || tenant.Ver != 1 {
		t.Fatalf("tenant = %+v; want group_tree_ver 1 from the create only and ver 1", tenant)
	}
}

func TestUpdateGroupOfAnUnknownOrDeletedGroupIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	insertDeletedGroup(t, s, "shop", "gone")
	name := "New"
	root := ""
	for _, u := range []store.GroupUpdate{{Name: &name}, {ParentID: &root}} {
		for _, id := range []string{"ghost", "gone"} {
			if err := s.UpdateGroup(t.Context(), "shop", id, u, itAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("UpdateGroup %s with parent change %v = %v; want not found", id, u.ParentID != nil, err)
			}
		}
	}
	if got := readGroupDoc(t, s, "shop", "gone"); got.Name != "Gone" || got.Ver != 2 {
		t.Fatalf("deleted group = %+v; want it unchanged", got)
	}
}

func TestGroupReadsADeletedGroupAndRefusesAnUnknownOne(t *testing.T) {
	s := testDatabase(t)
	insertDeletedGroup(t, s, "shop", "gone")
	got, err := s.Group(t.Context(), "shop", "gone")
	if err != nil || got.State != store.GroupDeleted || got.Name != "Gone" {
		t.Fatalf("Group gone = %+v, %v; want the deleted group", got, err)
	}
	for _, c := range []struct{ tenant, id string }{{"shop", "ghost"}, {"other", "gone"}} {
		if _, err := s.Group(t.Context(), c.tenant, c.id); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("Group %s/%s = %v; want not found", c.tenant, c.id, err)
		}
	}
}
