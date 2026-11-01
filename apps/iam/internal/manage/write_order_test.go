package manage_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type orderCase struct {
	name  string
	who   string
	grant store.Grant
	op    func(*manage.Service, auth.Caller) error
	want  error
}

func orderCases() []orderCase {
	ctx := context.Background()
	writer := perms("iam:roles.write", "iam:groups.read")
	binh := perms("iam:groups.members.write", "iam:groups.read")
	full := allIAMExcept()
	return []orderCase{
		{"create role with a typo permission by a non tenant-admin", "lan", writer, func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "x", Name: "X", Permissions: []string{"iam:acounts.read"}})
			return err
		}, apperr.ErrInvalidArgument},
		{"set groups with an unknown and a stronger group", "binh", binh, func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, "ha", []string{"ghost", "admins"})
		}, apperr.ErrNotFound},
		{"update the builtin tenant-admin role", "minh", full, func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateRole(ctx, c, authz.TenantAdminRole, store.RoleUpdate{Name: new("Boss")})
			return err
		}, apperr.ErrFailedPrecondition},
		{"delete the builtin tenant-admin role", "minh", full, func(s *manage.Service, c auth.Caller) error {
			_, err := s.DeleteRole(ctx, c, authz.TenantAdminRole)
			return err
		}, apperr.ErrFailedPrecondition},
		{"assign to an account of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			return s.AssignAccountRole(ctx, c, "far", "reader")
		}, apperr.ErrNotFound},
		{"assign a role of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			return s.AssignAccountRole(ctx, c, "ha", "far-role")
		}, apperr.ErrNotFound},
		{"get a role of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetRole(ctx, c, "far-role")
			return err
		}, apperr.ErrNotFound},
		{"delete a group of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			return s.DeleteGroup(ctx, c, "far-group")
		}, apperr.ErrNotFound},
		{"move under a group of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it-hcm", store.GroupUpdate{ParentID: new("far-group")})
			return err
		}, apperr.ErrNotFound},
		{"join a group of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, "ha", []string{"far-group"})
		}, apperr.ErrNotFound},
		{"read an account of another tenant", "minh", full, func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetAccount(ctx, c, "far")
			return err
		}, apperr.ErrNotFound},
	}
}

func TestStoreOutcomesComeBeforeEscalation(t *testing.T) {
	for _, tc := range orderCases() {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.grants[tc.who] = tc.grant
			svc, r := newService(w)
			err := tc.op(svc, caller(tc.who))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(r.audits) != 0 {
				t.Fatalf("audits = %v", r.audits)
			}
		})
	}
}

func TestCreateRoleTypoNamesThePermission(t *testing.T) {
	w := newWorld()
	w.grants["lan"] = store.Grant{TenantAdmin: true}
	svc, _ := newService(w)
	_, err := svc.CreateRole(context.Background(), caller("lan"), manage.NewRole{ID: "x", Name: "X", Permissions: []string{"iam:acounts.read"}})
	if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.Contains(err.Error(), "iam:acounts.read") {
		t.Fatalf("err = %v", err)
	}
}
