package manage_test

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (t *fakeTx) CreateRole(_ context.Context, r store.Role) error {
	if err := t.call("CreateRole"); err != nil {
		return err
	}
	for _, p := range r.Permissions {
		if !slices.Contains(authz.IAMPermissions, p) {
			return fmt.Errorf("%w: permission not in catalog: %s", apperr.ErrInvalidArgument, p)
		}
	}
	return nil
}

func (t *fakeTx) UpdateRole(_ context.Context, tenantID, id string, u store.RoleUpdate, _ time.Time, _ string) (store.Role, []string, error) {
	if err := t.call("UpdateRole"); err != nil {
		return store.Role{}, nil, err
	}
	r, err := t.r.w.editableRole(tenantID, id)
	if err != nil {
		return store.Role{}, nil, err
	}
	added := []string{}
	if u.Permissions != nil {
		for _, p := range *u.Permissions {
			if !slices.Contains(r.Permissions, p) {
				added = append(added, p)
			}
		}
		r.Permissions = *u.Permissions
	}
	if u.Name != nil {
		r.Name = *u.Name
	}
	r.Ver++
	return r, added, nil
}

func (t *fakeTx) DeleteRole(_ context.Context, tenantID, id string) (store.RoleRemoval, error) {
	if err := t.call("DeleteRole"); err != nil {
		return store.RoleRemoval{}, err
	}
	if _, err := t.r.w.editableRole(tenantID, id); err != nil {
		return store.RoleRemoval{}, err
	}
	return store.RoleRemoval{Accounts: 3, Groups: 1}, nil
}

func (t *fakeTx) Role(_ context.Context, tenantID, id string) (store.Role, error) {
	if err := t.call("Role"); err != nil {
		return store.Role{}, err
	}
	r, ok := t.r.w.roles[id]
	if !ok || !t.r.w.here(tenantID, id) {
		return store.Role{}, notFound("role")
	}
	return r, nil
}

func (t *fakeTx) RoleGrant(_ context.Context, tenantID, id string) (store.Grant, error) {
	if err := t.call("RoleGrant"); err != nil {
		return store.Grant{}, err
	}
	if _, ok := t.r.w.roles[id]; !ok || !t.r.w.here(tenantID, id) {
		return store.Grant{}, notFound("role")
	}
	return t.r.w.roleGrant(id), nil
}

func (t *fakeTx) AssignAccountRole(_ context.Context, _, account, roleID string, _ time.Time, _ string) (bool, error) {
	return !t.r.w.accountRoles[account+"|"+roleID], t.call("AssignAccountRole")
}

func (t *fakeTx) UnassignAccountRole(_ context.Context, _, account, roleID string) (bool, error) {
	return t.r.w.accountRoles[account+"|"+roleID], t.call("UnassignAccountRole")
}

func (t *fakeTx) AssignGroupRole(_ context.Context, _, groupID, roleID string, _ time.Time, _ string) (bool, error) {
	return !t.r.w.groupBinds[groupID+"|"+roleID], t.call("AssignGroupRole")
}

func (t *fakeTx) UnassignGroupRole(_ context.Context, _, groupID, roleID string) (bool, error) {
	return t.r.w.groupBinds[groupID+"|"+roleID], t.call("UnassignGroupRole")
}

func (t *fakeTx) Account(_ context.Context, tenantID, id string) (store.AccountView, error) {
	if err := t.call("Account"); err != nil {
		return store.AccountView{}, err
	}
	a, ok := t.r.w.accounts[id]
	if !ok || !t.r.w.here(tenantID, id) {
		return store.AccountView{}, notFound("account")
	}
	return a, nil
}

func (t *fakeTx) Accounts(_ context.Context, _ string, after string, limit int) ([]store.AccountView, string, error) {
	t.r.pages = append(t.r.pages, fmt.Sprintf("accounts %s %d", after, limit))
	return []store.AccountView{t.r.w.accounts["ha"], t.r.w.accounts["lan"]}, "lan", t.call("Accounts")
}

func (t *fakeTx) Roles(_ context.Context, _ string, after string, limit int) ([]store.Role, string, error) {
	t.r.pages = append(t.r.pages, fmt.Sprintf("roles %s %d", after, limit))
	return []store.Role{t.r.w.roles["reader"]}, "", t.call("Roles")
}

func (t *fakeTx) Groups(_ context.Context, _ string, after string, limit int) ([]store.GroupView, string, error) {
	t.r.pages = append(t.r.pages, fmt.Sprintf("groups %s %d", after, limit))
	return []store.GroupView{{Group: t.r.w.groups["it"].Group, RoleIDs: []string{"it-admin"}}}, "it", t.call("Groups")
}

func (w *world) editableRole(tenantID, id string) (store.Role, error) {
	r, ok := w.roles[id]
	if !ok || !w.here(tenantID, id) {
		return store.Role{}, notFound("role")
	}
	if r.Builtin {
		return store.Role{}, fmt.Errorf("%w: builtin role cannot change", apperr.ErrFailedPrecondition)
	}
	return r, nil
}
