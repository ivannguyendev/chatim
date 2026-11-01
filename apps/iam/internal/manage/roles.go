package manage

import (
	"context"
	"strconv"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

type NewRole struct {
	ID          string
	Name        string
	Permissions []string
}

func (s *Service) CreateRole(ctx context.Context, c auth.Caller, in NewRole) (store.Role, error) {
	if err := authz.ValidateRoleID(in.ID); err != nil {
		return store.Role{}, err
	}
	name, err := directory.NormalizeRoleName(in.Name)
	if err != nil {
		return store.Role{}, err
	}
	perms, err := authz.ParseAll(in.Permissions)
	if err != nil {
		return store.Role{}, err
	}
	var role store.Role
	err = s.write(ctx, c, "CreateRole", false, func(st *step) error {
		if err := authz.CheckRoleWrite(st.grant, perms); err != nil {
			return err
		}
		role = store.Role{
			TenantID: c.Tenant, ID: in.ID, Name: name, Ver: 1, Permissions: permissionStrings(perms),
			CreatedAt: st.now, UpdatedAt: st.now, UpdatedBy: c.Account,
		}
		if err := st.tx.CreateRole(ctx, role); err != nil {
			return err
		}
		return st.audit(ctx, "role_create", "", map[string]string{"role": in.ID})
	})
	if err != nil {
		return store.Role{}, err
	}
	return role, nil
}

func (s *Service) UpdateRole(ctx context.Context, c auth.Caller, id string, u store.RoleUpdate) (store.Role, error) {
	update, err := validRoleUpdate(id, u)
	if err != nil {
		return store.Role{}, err
	}
	var role store.Role
	err = s.write(ctx, c, "UpdateRole", false, func(st *step) error {
		updated, added, err := st.tx.UpdateRole(ctx, c.Tenant, id, update, st.now, c.Account)
		if err != nil {
			return err
		}
		if err := authz.CheckRoleWrite(st.grant, toPermissions(added)); err != nil {
			return err
		}
		role = updated
		return st.audit(ctx, "role_update", "", map[string]string{"role": id})
	})
	if err != nil {
		return store.Role{}, err
	}
	return role, nil
}

func validRoleUpdate(id string, u store.RoleUpdate) (store.RoleUpdate, error) {
	if err := authz.ValidateRoleID(id); err != nil {
		return store.RoleUpdate{}, err
	}
	if u.Name == nil && u.Permissions == nil {
		return store.RoleUpdate{}, ErrNoChange
	}
	var out store.RoleUpdate
	if u.Name != nil {
		name, err := directory.NormalizeRoleName(*u.Name)
		if err != nil {
			return store.RoleUpdate{}, err
		}
		out.Name = &name
	}
	if u.Permissions != nil {
		perms, err := authz.ParseAll(*u.Permissions)
		if err != nil {
			return store.RoleUpdate{}, err
		}
		list := permissionStrings(perms)
		out.Permissions = &list
	}
	return out, nil
}

func (s *Service) DeleteRole(ctx context.Context, c auth.Caller, id string) (store.RoleRemoval, error) {
	if err := authz.ValidateRoleID(id); err != nil {
		return store.RoleRemoval{}, err
	}
	var removed store.RoleRemoval
	err := s.write(ctx, c, "DeleteRole", false, func(st *step) error {
		var err error
		if removed, err = st.tx.DeleteRole(ctx, c.Tenant, id); err != nil {
			return err
		}
		return st.audit(ctx, "role_delete", "", map[string]string{
			"role": id, "accounts": strconv.Itoa(removed.Accounts), "groups": strconv.Itoa(removed.Groups),
		})
	})
	if err != nil {
		return store.RoleRemoval{}, err
	}
	return removed, nil
}

func (s *Service) GetRole(ctx context.Context, c auth.Caller, id string) (store.Role, error) {
	if err := authz.ValidateRoleID(id); err != nil {
		return store.Role{}, err
	}
	var role store.Role
	err := s.read(ctx, c, "GetRole", func(st *step) error {
		var err error
		role, err = st.tx.Role(ctx, c.Tenant, id)
		return err
	})
	if err != nil {
		return store.Role{}, err
	}
	return role, nil
}

func (s *Service) ListRoles(ctx context.Context, c auth.Caller, pageToken string) ([]store.Role, string, error) {
	if err := validPageToken(pageToken, authz.ValidateRoleID); err != nil {
		return nil, "", err
	}
	var (
		page []store.Role
		next string
	)
	err := s.read(ctx, c, "ListRoles", func(st *step) error {
		var err error
		page, next, err = st.tx.Roles(ctx, c.Tenant, pageToken, PageSize)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return page, next, nil
}

func permissionStrings(perms []authz.Permission) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		out = append(out, string(p))
	}
	return out
}

func toPermissions(list []string) []authz.Permission {
	out := make([]authz.Permission, 0, len(list))
	for _, p := range list {
		out = append(out, authz.Permission(p))
	}
	return out
}
