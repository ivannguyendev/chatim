package manage

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
)

func (s *Service) AssignAccountRole(ctx context.Context, c auth.Caller, accountID, roleID string) error {
	if err := firstError(validAccountID(accountID), authz.ValidateRoleID(roleID)); err != nil {
		return err
	}
	return s.write(ctx, c, "AssignAccountRole", false, func(st *step) error {
		if err := st.tx.LockAccount(ctx, c.Tenant, accountID); err != nil {
			return err
		}
		role, err := st.roleGrant(ctx, roleID)
		if err != nil {
			return err
		}
		target, err := st.accountGrant(ctx, accountID)
		if err != nil {
			return err
		}
		if err := authz.CheckAccountRoleAssign(st.grant, role, target); err != nil {
			return err
		}
		added, err := st.tx.AssignAccountRole(ctx, c.Tenant, accountID, roleID, st.now, c.Account)
		if err != nil || !added {
			return err
		}
		return st.audit(ctx, "account_role_assign", accountID, map[string]string{"role": roleID})
	})
}

func (s *Service) UnassignAccountRole(ctx context.Context, c auth.Caller, accountID, roleID string) error {
	if err := firstError(validAccountID(accountID), authz.ValidateRoleID(roleID)); err != nil {
		return err
	}
	return s.write(ctx, c, "UnassignAccountRole", false, func(st *step) error {
		if err := st.tx.LockAccount(ctx, c.Tenant, accountID); err != nil {
			return err
		}
		target, err := st.accountGrant(ctx, accountID)
		if err != nil {
			return err
		}
		if err := authz.CheckAccountRoleUnassign(st.grant, target); err != nil {
			return err
		}
		removed, err := st.tx.UnassignAccountRole(ctx, c.Tenant, accountID, roleID)
		if err != nil || !removed {
			return err
		}
		return st.audit(ctx, "account_role_unassign", accountID, map[string]string{"role": roleID})
	})
}

func (s *Service) AssignGroupRole(ctx context.Context, c auth.Caller, groupID, roleID string) error {
	if err := firstError(directory.ValidateGroupID(groupID), authz.ValidateRoleID(roleID)); err != nil {
		return err
	}
	return s.write(ctx, c, "AssignGroupRole", false, func(st *step) error {
		if _, err := st.tx.GroupNode(ctx, c.Tenant, groupID); err != nil {
			return err
		}
		role, err := st.roleGrant(ctx, roleID)
		if err != nil {
			return err
		}
		if err := authz.CheckGroupRoleAssign(st.grant, role); err != nil {
			return err
		}
		added, err := st.tx.AssignGroupRole(ctx, c.Tenant, groupID, roleID, st.now, c.Account)
		if err != nil || !added {
			return err
		}
		return st.audit(ctx, "group_role_assign", "", map[string]string{"group": groupID, "role": roleID})
	})
}

func (s *Service) UnassignGroupRole(ctx context.Context, c auth.Caller, groupID, roleID string) error {
	if err := firstError(directory.ValidateGroupID(groupID), authz.ValidateRoleID(roleID)); err != nil {
		return err
	}
	return s.write(ctx, c, "UnassignGroupRole", false, func(st *step) error {
		if _, err := st.tx.GroupNode(ctx, c.Tenant, groupID); err != nil {
			return err
		}
		if err := authz.CheckGroupRoleUnassign(st.grant, roleID == authz.TenantAdminRole); err != nil {
			return err
		}
		removed, err := st.tx.UnassignGroupRole(ctx, c.Tenant, groupID, roleID)
		if err != nil || !removed {
			return err
		}
		return st.audit(ctx, "group_role_unassign", "", map[string]string{"group": groupID, "role": roleID})
	})
}
