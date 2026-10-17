package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

func RunGroups(t *testing.T, open func(t *testing.T) store.Groups) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, s store.Groups)
	}{
		{"SetStoresGroupsAndReportsChanges", groupsSet},
		{"OlderVerIsIgnored", groupsOlderIgnored},
		{"InactiveGroupStaysListedAsNotTaggable", groupsInactive},
		{"AllIsPerTenant", groupsTenants},
		{"InvalidCallsAreRejected", groupsInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func grp(id, name string, active bool, ver uint64) domain.Group {
	return domain.Group{Tenant: tenant, ID: id, Name: name, Active: active, Ver: ver}
}

func mustSetGroups(t *testing.T, s store.Groups, want []string, items ...domain.Group) {
	t.Helper()
	got, err := s.Set(t.Context(), items)
	if err != nil || !slices.Equal(sorted(got), sorted(want)) {
		t.Fatalf("Set(%+v) = %v, %v; want changed %v", items, got, err, want)
	}
}

func assertGroups(t *testing.T, s store.Groups, ten string, want ...domain.Group) {
	t.Helper()
	got, err := s.All(t.Context(), ten)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("All(%s) = %+v, %v; want %+v", ten, got, err, want)
	}
}

func groupsSet(t *testing.T, s store.Groups) {
	mustSetGroups(t, s, []string{"team-sale", "hr"}, grp("team-sale", "Phòng Kinh doanh", true, 50), grp("hr", "HR", true, 3))
	assertGroups(t, s, tenant, grp("hr", "HR", true, 3), grp("team-sale", "Phòng Kinh doanh", true, 50))
	mustSetGroups(t, s, nil, grp("team-sale", "Phòng Kinh doanh", true, 50), grp("hr", "HR", true, 3))
	mustSetGroups(t, s, nil, grp("hr", "HR", true, 4))
	mustSetGroups(t, s, []string{"hr"}, grp("hr", "Nhân sự", true, 5))
	assertGroups(t, s, tenant, grp("hr", "Nhân sự", true, 5), grp("team-sale", "Phòng Kinh doanh", true, 50))
}

func groupsOlderIgnored(t *testing.T, s store.Groups) {
	mustSetGroups(t, s, []string{"hr"}, grp("hr", "HR", true, 5))
	mustSetGroups(t, s, nil, grp("hr", "Old HR", false, 4))
	assertGroups(t, s, tenant, grp("hr", "HR", true, 5))
}

func groupsInactive(t *testing.T, s store.Groups) {
	mustSetGroups(t, s, []string{"hr-internal"}, grp("hr-internal", "HR nội bộ", true, 1))
	mustSetGroups(t, s, []string{"hr-internal"}, grp("hr-internal", "HR nội bộ", false, 2))
	assertGroups(t, s, tenant, grp("hr-internal", "HR nội bộ", false, 2))
	mustSetGroups(t, s, []string{"hr-internal"}, grp("hr-internal", "HR nội bộ", true, 3))
	assertGroups(t, s, tenant, grp("hr-internal", "HR nội bộ", true, 3))
}

func groupsTenants(t *testing.T, s store.Groups) {
	foreign := grp("ops", "Ops", true, 9)
	foreign.Tenant = otherTenant
	mustSetGroups(t, s, []string{"ops", "ops"}, grp("ops", "Vận hành", true, 1), foreign)
	assertGroups(t, s, tenant, grp("ops", "Vận hành", true, 1))
	assertGroups(t, s, otherTenant, foreign)
	assertGroups(t, s, "initech")
}

func groupsInvalid(t *testing.T, s store.Groups) {
	_, err := s.Set(t.Context(), []domain.Group{grp("ops", "Ops", true, 1), grp("ops", "Ops", true, 2)})
	assertInvalid(t, "Set(group twice)", err)
	_, err = s.Set(t.Context(), []domain.Group{grp("hr", "HR", true, 1), grp("ops", " ", true, 1)})
	assertInvalid(t, "Set(blank name)", err)
	_, err = s.All(t.Context(), "")
	assertInvalid(t, "All(no tenant)", err)
	assertGroups(t, s, tenant)
}
