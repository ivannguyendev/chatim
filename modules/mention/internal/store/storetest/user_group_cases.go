package storetest

import (
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
)

func RunUserGroups(t *testing.T, open func(t *testing.T) store.UserGroups) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, s store.UserGroups)
	}{
		{"SetStoresTheFullDedupedSortedListAndReportsChanges", userGroupsSet},
		{"OlderVerIsIgnored", userGroupsOlderIgnored},
		{"MovingBetweenGroupsIsOneWrite", userGroupsMove},
		{"EmptyGroupsKeepTheDocAndStillBlockOlderVers", userGroupsEmpty},
		{"MembersOfGroupPagesByUserID", userGroupsMembersPage},
		{"TenantsStayApart", userGroupsTenants},
		{"InvalidCallsAreRejected", userGroupsInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func ug(user string, ver uint64, groups ...string) domain.UserGroups {
	return domain.UserGroups{Tenant: tenant, User: user, Groups: groups, Ver: ver}
}

func mustSetUsers(t *testing.T, s store.UserGroups, want []string, items ...domain.UserGroups) {
	t.Helper()
	got, err := s.Set(t.Context(), items)
	if err != nil || !slices.Equal(sorted(got), sorted(want)) {
		t.Fatalf("Set(%+v) = %v, %v; want changed %v", items, got, err, want)
	}
}

func assertUserGroups(t *testing.T, s store.UserGroups, ten string, users []string, want map[string][]string) {
	t.Helper()
	got, err := s.Of(t.Context(), ten, users)
	if err != nil || !maps.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("Of(%s, %v) = %v, %v; want %v", ten, users, got, err, want)
	}
}

func assertGroupMembers(t *testing.T, s store.UserGroups, ten, group, after string, limit int, want ...string) {
	t.Helper()
	got, err := s.MembersOfGroup(t.Context(), ten, group, after, limit)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("MembersOfGroup(%s, %s, after %q, %d) = %v, %v; want %v", ten, group, after, limit, got, err, want)
	}
}

func userGroupsSet(t *testing.T, s store.UserGroups) {
	mustSetUsers(t, s, []string{"ha", "lan"}, ug("ha", 5, "sale", "ops", "sale"), ug("lan", 1))
	assertUserGroups(t, s, tenant, []string{"ha", "lan", "nobody"}, map[string][]string{"ha": {"ops", "sale"}, "lan": {}})
	mustSetUsers(t, s, nil, ug("ha", 5, "sale", "ops"), ug("lan", 1))
	mustSetUsers(t, s, nil, ug("ha", 6, "ops", "sale"))
	mustSetUsers(t, s, nil, ug("ha", 5, "hr"))
	assertUserGroups(t, s, tenant, []string{"ha"}, map[string][]string{"ha": {"ops", "sale"}})
}

func userGroupsOlderIgnored(t *testing.T, s store.UserGroups) {
	mustSetUsers(t, s, []string{"ha"}, ug("ha", 1004, "manager"))
	mustSetUsers(t, s, nil, ug("ha", 1002, "staff"))
	assertUserGroups(t, s, tenant, []string{"ha"}, map[string][]string{"ha": {"manager"}})
	assertGroupMembers(t, s, tenant, "staff", "", 10)
}

func userGroupsMove(t *testing.T, s store.UserGroups) {
	mustSetUsers(t, s, []string{"ha"}, ug("ha", 1002, "staff", "sale"))
	mustSetUsers(t, s, []string{"ha"}, ug("ha", 1003, "manager", "sale"))
	assertGroupMembers(t, s, tenant, "staff", "", 10)
	assertGroupMembers(t, s, tenant, "manager", "", 10, "ha")
	assertGroupMembers(t, s, tenant, "sale", "", 10, "ha")
}

func userGroupsEmpty(t *testing.T, s store.UserGroups) {
	mustSetUsers(t, s, []string{"ha"}, ug("ha", 1003, "manager"))
	mustSetUsers(t, s, []string{"ha"}, ug("ha", 1004))
	assertUserGroups(t, s, tenant, []string{"ha"}, map[string][]string{"ha": {}})
	mustSetUsers(t, s, nil, ug("ha", 1003, "manager"))
	assertUserGroups(t, s, tenant, []string{"ha"}, map[string][]string{"ha": {}})
	assertGroupMembers(t, s, tenant, "manager", "", 10)
}

func userGroupsMembersPage(t *testing.T, s store.UserGroups) {
	mustSetUsers(t, s, []string{"u05", "u01", "u03", "u02", "u04"},
		ug("u05", 1, "ops"), ug("u01", 1, "ops", "hr"), ug("u03", 1, "hr"), ug("u02", 1, "ops"), ug("u04", 1, "ops"))
	assertGroupMembers(t, s, tenant, "ops", "", 2, "u01", "u02")
	assertGroupMembers(t, s, tenant, "ops", "u02", 2, "u04", "u05")
	assertGroupMembers(t, s, tenant, "ops", "u05", 2)
	assertGroupMembers(t, s, tenant, "hr", "", 10, "u01", "u03")
	assertGroupMembers(t, s, tenant, "none", "", 10)
}

func userGroupsTenants(t *testing.T, s store.UserGroups) {
	foreign := ug("ha", 9, "ops")
	foreign.Tenant = otherTenant
	mustSetUsers(t, s, []string{"ha", "ha"}, ug("ha", 5, "sale"), foreign)
	assertUserGroups(t, s, tenant, []string{"ha"}, map[string][]string{"ha": {"sale"}})
	assertUserGroups(t, s, otherTenant, []string{"ha"}, map[string][]string{"ha": {"ops"}})
	assertGroupMembers(t, s, tenant, "ops", "", 10)
	assertGroupMembers(t, s, otherTenant, "ops", "", 10, "ha")
}

func userGroupsInvalid(t *testing.T, s store.UserGroups) {
	_, err := s.Set(t.Context(), []domain.UserGroups{ug("ha", 1, "ops"), ug("ha", 2)})
	assertInvalid(t, "Set(user twice)", err)
	_, err = s.Set(t.Context(), []domain.UserGroups{ug("lan", 1, "ops"), ug("ha", 0)})
	assertInvalid(t, "Set(ver 0)", err)
	_, err = s.Of(t.Context(), "", []string{"ha"})
	assertInvalid(t, "Of(no tenant)", err)
	_, err = s.MembersOfGroup(t.Context(), tenant, "a b", "", 10)
	assertInvalid(t, "MembersOfGroup(bad group)", err)
	_, err = s.MembersOfGroup(t.Context(), tenant, "ops", "", 0)
	assertInvalid(t, "MembersOfGroup(limit 0)", err)
	assertUserGroups(t, s, tenant, []string{"ha", "lan"}, map[string][]string{})
}
