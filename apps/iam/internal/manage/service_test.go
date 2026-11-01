package manage_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type rpcCase struct {
	rpc      string
	read     bool
	treeLock bool
	action   string
	op       func(*manage.Service, auth.Caller) error
}

func rpcCases() []rpcCase {
	ctx := context.Background()
	return []rpcCase{
		{"CreateRole", false, false, "role_create", func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "new-role", Name: "New", Permissions: []string{"iam:groups.read"}})
			return err
		}},
		{"UpdateRole", false, false, "role_update", func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateRole(ctx, c, "reader", store.RoleUpdate{Name: new("Readers")})
			return err
		}},
		{"DeleteRole", false, false, "role_delete", func(s *manage.Service, c auth.Caller) error {
			_, err := s.DeleteRole(ctx, c, "reader")
			return err
		}},
		{"GetRole", true, false, "", func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetRole(ctx, c, "reader")
			return err
		}},
		{"ListRoles", true, false, "", func(s *manage.Service, c auth.Caller) error {
			_, _, err := s.ListRoles(ctx, c, "")
			return err
		}},
		{"AssignAccountRole", false, false, "account_role_assign", func(s *manage.Service, c auth.Caller) error {
			return s.AssignAccountRole(ctx, c, "ha", "reader")
		}},
		{"UnassignAccountRole", false, false, "account_role_unassign", func(s *manage.Service, c auth.Caller) error {
			return s.UnassignAccountRole(ctx, c, "ha", "it-admin")
		}},
		{"AssignGroupRole", false, false, "group_role_assign", func(s *manage.Service, c auth.Caller) error {
			return s.AssignGroupRole(ctx, c, "it-hcm", "reader")
		}},
		{"UnassignGroupRole", false, false, "group_role_unassign", func(s *manage.Service, c auth.Caller) error {
			return s.UnassignGroupRole(ctx, c, "it", "it-admin")
		}},
		{"CreateGroup", false, true, "group_create", func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateGroup(ctx, c, manage.NewGroup{ID: "it-hn", Name: "IT HN", ParentID: "it"})
			return err
		}},
		{"UpdateGroup", false, false, "group_update", func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it-hcm", store.GroupUpdate{Name: new("IT HCM")})
			return err
		}},
		{"DeleteGroup", false, true, "group_delete", func(s *manage.Service, c auth.Caller) error {
			return s.DeleteGroup(ctx, c, "it-hcm")
		}},
		{"ListGroups", true, false, "", func(s *manage.Service, c auth.Caller) error {
			_, _, err := s.ListGroups(ctx, c, "")
			return err
		}},
		{"SetAccountGroups", false, false, "account_groups_set", func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, "ha", []string{"it-hcm", "cong-ty"})
		}},
		{"GetAccount", true, false, "", func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetAccount(ctx, c, "ha")
			return err
		}},
		{"ListAccounts", true, false, "", func(s *manage.Service, c auth.Caller) error {
			_, _, err := s.ListAccounts(ctx, c, "")
			return err
		}},
	}
}

func TestEachRPCNeedsItsPermission(t *testing.T) {
	cases := rpcCases()
	if len(cases) != len(authz.Required) {
		t.Fatalf("table has %d rpcs, authz.Required has %d", len(cases), len(authz.Required))
	}
	for _, tc := range cases {
		t.Run(tc.rpc, func(t *testing.T) {
			w := newWorld()
			w.grants["minh"] = allIAMExcept(string(authz.Required[tc.rpc]))
			svc, r := newService(w)
			err := tc.op(svc, caller("minh"))
			if !errors.Is(err, apperr.ErrPermissionDenied) || !strings.Contains(err.Error(), string(authz.Required[tc.rpc])) {
				t.Fatalf("err = %v, want permission denied naming %s", err, authz.Required[tc.rpc])
			}
			if !slices.Equal(r.calls, []string{"RunningSession", "AccountGrant"}) || len(r.audits) != 0 {
				t.Fatalf("calls = %v, audits = %v", r.calls, r.audits)
			}

			svc, _ = newService(newWorld())
			if err := tc.op(svc, caller("minh")); err != nil {
				t.Fatalf("with every iam permission: %v", err)
			}
		})
	}
}

func TestSessionIsCheckedBeforeAnyRead(t *testing.T) {
	for _, tc := range rpcCases() {
		t.Run(tc.rpc, func(t *testing.T) {
			w := newWorld()
			delete(w.sessions, "minh|s1")
			svc, r := newService(w)
			err := tc.op(svc, caller("minh"))
			if !errors.Is(err, auth.ErrSessionInvalid) || !errors.Is(err, apperr.ErrUnauthenticated) {
				t.Fatalf("err = %v, want session invalid", err)
			}
			if !slices.Equal(r.calls, []string{"RunningSession"}) {
				t.Fatalf("calls = %v, want only RunningSession", r.calls)
			}
		})
	}
}

func TestSessionStoreErrorIsReturned(t *testing.T) {
	w := newWorld()
	w.fail["RunningSession"] = apperr.ErrUnavailable
	svc, _ := newService(w)
	if _, err := svc.GetRole(context.Background(), caller("minh"), "reader"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestPermissionDeniedComesBeforeNotFound(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*manage.Service, auth.Caller) error{
		"AssignAccountRole": func(s *manage.Service, c auth.Caller) error { return s.AssignAccountRole(ctx, c, "ghost", "ghost") },
		"UnassignGroupRole": func(s *manage.Service, c auth.Caller) error {
			return s.UnassignGroupRole(ctx, c, "ghost", authz.TenantAdminRole)
		},
		"DeleteGroup": func(s *manage.Service, c auth.Caller) error { return s.DeleteGroup(ctx, c, "ghost") },
		"DeleteRole": func(s *manage.Service, c auth.Caller) error {
			_, err := s.DeleteRole(ctx, c, "ghost")
			return err
		},
		"GetAccount": func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetAccount(ctx, c, "ghost")
			return err
		},
		"SetAccountGroups": func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, "ghost", []string{"admins"})
		},
	}
	for rpc, op := range ops {
		t.Run(rpc, func(t *testing.T) {
			w := newWorld()
			w.grants["minh"] = allIAMExcept(string(authz.Required[rpc]))
			svc, _ := newService(w)
			if err := op(svc, caller("minh")); !errors.Is(err, apperr.ErrPermissionDenied) {
				t.Fatalf("without permission: %v", err)
			}
			w.grants["minh"] = allIAMExcept()
			if err := op(svc, caller("minh")); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("with permission, unknown id must be not found before any escalation rule: %v", err)
			}
		})
	}
}

func TestOneAuditPerWriteAndNoneOnDenial(t *testing.T) {
	for _, tc := range rpcCases() {
		t.Run(tc.rpc, func(t *testing.T) {
			svc, r := newService(newWorld())
			if err := tc.op(svc, caller("minh")); err != nil {
				t.Fatal(err)
			}
			if tc.read {
				if len(r.audits) != 0 || len(r.wrote()) != 0 {
					t.Fatalf("read wrote %v", r.wrote())
				}
				return
			}
			if len(r.audits) != 1 {
				t.Fatalf("audits = %v", r.audits)
			}
			a := r.audits[0]
			if a.Action != tc.action || a.TenantID != tenant || a.Actor != "minh" || a.SessionID != "s1" || a.IP != "10.0.0.9" || !a.At.Equal(now) {
				t.Fatalf("audit = %+v", a)
			}
		})
	}
}

func TestAuditDetails(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		op      func(*manage.Service, auth.Caller) error
		account string
		detail  map[string]string
	}{
		{"role create", func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "x", Name: "X"})
			return err
		}, "", map[string]string{"role": "x"}},
		{"role delete counts unassigned", func(s *manage.Service, c auth.Caller) error {
			_, err := s.DeleteRole(ctx, c, "reader")
			return err
		}, "", map[string]string{"role": "reader", "accounts": "3", "groups": "1"}},
		{"account role", func(s *manage.Service, c auth.Caller) error { return s.AssignAccountRole(ctx, c, "ha", "reader") },
			"ha", map[string]string{"role": "reader"}},
		{"group role", func(s *manage.Service, c auth.Caller) error { return s.AssignGroupRole(ctx, c, "it-hcm", "reader") },
			"", map[string]string{"group": "it-hcm", "role": "reader"}},
		{"group delete counts roles", func(s *manage.Service, c auth.Caller) error { return s.DeleteGroup(ctx, c, "it-hcm") },
			"", map[string]string{"group": "it-hcm", "roles": "2"}},
		{"account groups", func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, "ha", []string{"it-hcm", "cong-ty"})
		}, "ha", map[string]string{"groups": "it-hcm,cong-ty"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, r := newService(newWorld())
			if err := tc.op(svc, caller("minh")); err != nil {
				t.Fatal(err)
			}
			a := r.audits[0]
			if a.AccountID != tc.account || len(a.Detail) != len(tc.detail) {
				t.Fatalf("audit = %+v", a)
			}
			for k, v := range tc.detail {
				if a.Detail[k] != v {
					t.Fatalf("detail[%s] = %q, want %q", k, a.Detail[k], v)
				}
			}
		})
	}
}

func TestNoAuditWhenNothingChanges(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.accountRoles["ha|reader"] = true
	svc, r := newService(w)
	c := caller("minh")
	if err := svc.AssignAccountRole(ctx, c, "ha", "reader"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UnassignAccountRole(ctx, c, "ha", "hr-admin"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignGroupRole(ctx, c, "it", "reader"); err != nil {
		t.Fatal(err)
	}
	w.groupBinds["it|reader"] = true
	if err := svc.AssignGroupRole(ctx, c, "it", "reader"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UnassignGroupRole(ctx, c, "it", "hr-admin"); err != nil {
		t.Fatal(err)
	}
	if len(r.audits) != 1 || r.audits[0].Action != "group_role_assign" {
		t.Fatalf("audits = %v", r.audits)
	}
}

func TestTreeLockOnlyForMovesCreatesUnderAParentAndDeletes(t *testing.T) {
	for _, tc := range rpcCases() {
		t.Run(tc.rpc, func(t *testing.T) {
			svc, r := newService(newWorld())
			if err := tc.op(svc, caller("minh")); err != nil {
				t.Fatal(err)
			}
			want := store.AdminTxOptions{Tenant: tenant, TreeLock: tc.treeLock, ReadOnly: tc.read}
			if len(r.runs) != 1 || r.runs[0] != want {
				t.Fatalf("runs = %+v, want %+v", r.runs, want)
			}
		})
	}
	ctx := context.Background()
	extra := []struct {
		name string
		op   func(*manage.Service, auth.Caller) error
		lock bool
	}{
		{"create root group", func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateGroup(ctx, c, manage.NewGroup{ID: "sales", Name: "Sales"})
			return err
		}, false},
		{"move under a parent", func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it-hcm", store.GroupUpdate{ParentID: new("cong-ty")})
			return err
		}, true},
		{"move to the root", func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it-hcm", store.GroupUpdate{ParentID: new("")})
			return err
		}, true},
		{"mentionable only", func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it-hcm", store.GroupUpdate{Mentionable: new(false)})
			return err
		}, false},
	}
	for _, tc := range extra {
		t.Run(tc.name, func(t *testing.T) {
			svc, r := newService(newWorld())
			if err := tc.op(svc, caller("minh")); err != nil {
				t.Fatal(err)
			}
			if len(r.runs) != 1 || r.runs[0].TreeLock != tc.lock || r.runs[0].ReadOnly {
				t.Fatalf("runs = %+v", r.runs)
			}
		})
	}
}

func TestReadsUseAReadOnlyTransaction(t *testing.T) {
	for _, tc := range rpcCases() {
		if !tc.read {
			continue
		}
		t.Run(tc.rpc, func(t *testing.T) {
			svc, r := newService(newWorld())
			if err := tc.op(svc, caller("minh")); err != nil {
				t.Fatal(err)
			}
			if len(r.runs) != 1 || !r.runs[0].ReadOnly || r.runs[0].TreeLock || len(r.wrote()) != 0 {
				t.Fatalf("runs = %+v, writes = %v", r.runs, r.wrote())
			}
		})
	}
}
