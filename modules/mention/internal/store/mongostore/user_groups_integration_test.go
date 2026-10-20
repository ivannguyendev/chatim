package mongostore

import (
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
)

const groupMembersIndex = "tenant_1_groups_1_user_id_1"

func seedUserGroups(t *testing.T, s *UserGroups, tenant string, n int) {
	t.Helper()
	items := make([]domain.UserGroups, n)
	for i := range items {
		items[i] = domain.UserGroups{Tenant: tenant, User: fmt.Sprintf("u%03d", i), Groups: []string{"all-staff", fmt.Sprintf("team-%d", i%5)}, Ver: 1, UpdatedBy: "hr-sync"}
	}
	if _, err := s.Set(t.Context(), items); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func TestUserGroupsOfReadsBoundedClusteredRanges(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	s := NewUserGroups(db)
	seedUserGroups(t, s, "acme", 200)
	seedUserGroups(t, s, "globex", 50)
	users := []string{"u007", "u008", "u150", "u042", "nobody"}
	filter := userGroupsFilter("acme", users)
	if raw, _ := bson.Marshal(filter); bson.Raw(raw).Lookup("_id", "$in").Type != 0 {
		t.Fatalf("filter %v uses $in on the clustered _id", filter)
	}
	p := explain(t, db, findCmd(userGroupsCollection, filter, nil, 0))
	assertClusteredBounded(t, "Of", p, len(users))
	if p.returned != 4 {
		t.Fatalf("Of explain returned %d, want 4", p.returned)
	}
	assertClusteredBounded(t, "Of one user", explain(t, db, findCmd(userGroupsCollection, userGroupsFilter("acme", []string{"u010"}), nil, 0)), 1)
}

func TestMembersOfGroupScansTheGroupIndexInUserOrder(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	s := NewUserGroups(db)
	seedUserGroups(t, s, "acme", 200)
	sort := bson.D{{Key: "user_id", Value: 1}}
	for name, after := range map[string]string{"first page": "", "next page": "u100"} {
		p := explain(t, db, findCmd(userGroupsCollection, groupMembersFilter("acme", "team-3", after), sort, 10))
		assertOnlyIndex(t, name, p, groupMembersIndex)
		if p.examined > 10 {
			t.Fatalf("%s: examined %d docs for a page of 10", name, p.examined)
		}
	}
	got, err := s.MembersOfGroup(t.Context(), "acme", "team-3", "u100", 3)
	if err != nil || fmt.Sprint(got) != "[u103 u108 u113]" {
		t.Fatalf("MembersOfGroup = %v, %v", got, err)
	}
}

func TestSetKeepsTheCaller(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	s := NewUserGroups(db)
	seedUserGroups(t, s, "acme", 1)
	older := domain.UserGroups{Tenant: "acme", User: "u000", Groups: []string{"x"}, Ver: 1, UpdatedBy: "replayer"}
	if changed, err := s.Set(t.Context(), []domain.UserGroups{older}); err != nil || len(changed) != 0 {
		t.Fatalf("Set(same ver) = %v, %v", changed, err)
	}
	var doc bson.M
	if err := db.Collection(userGroupsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: tenantID("acme", "u000")}}).Decode(&doc); err != nil {
		t.Fatalf("read doc: %v", err)
	}
	if doc["updated_by"] != "hr-sync" || doc["tenant"] != "acme" || doc["user_id"] != "u000" || doc["ver"] != int64(1) || doc["updated_at"] == nil {
		t.Fatalf("stored doc = %v, want updated_by hr-sync, ver 1 and an update time", doc)
	}
}

func TestGroupsAllReadsOneClusteredRange(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	g := NewGroups(db)
	var items []domain.Group
	for _, tenant := range []string{"acm", "acme", "acme1", "acmf"} {
		for i := range 20 {
			items = append(items, domain.Group{Tenant: tenant, ID: fmt.Sprintf("g%02d", i), Name: "G", Active: true, Ver: 1})
		}
	}
	if _, err := g.Set(t.Context(), items); err != nil {
		t.Fatalf("Set: %v", err)
	}
	p := explain(t, db, findCmd(groupsCollection, tenantRange("acme"), nil, 0))
	assertClusteredBounded(t, "All", p, 20)
	if p.returned != 20 {
		t.Fatalf("All explain returned %d, want 20", p.returned)
	}
}
