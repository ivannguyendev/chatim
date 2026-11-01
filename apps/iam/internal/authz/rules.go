package authz

import (
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrUnknownRPC       = fmt.Errorf("%w: rpc has no required permission", apperr.ErrPermissionDenied)
	ErrRoleGrantsMore   = fmt.Errorf("%w: role grants permissions the caller lacks", apperr.ErrPermissionDenied)
	ErrTargetHoldsMore  = fmt.Errorf("%w: target holds permissions the caller lacks", apperr.ErrPermissionDenied)
	ErrGroupsGrantMore  = fmt.Errorf("%w: new groups grant permissions the caller lacks", apperr.ErrPermissionDenied)
	ErrParentGrantsMore = fmt.Errorf("%w: new parent chain grants permissions the caller lacks", apperr.ErrPermissionDenied)
	ErrTenantAdminOnly  = fmt.Errorf("%w: only tenant-admin may change tenant-admin", apperr.ErrPermissionDenied)
)

func Require(caller Grant, rpc string) error {
	p, ok := Required[rpc]
	if !ok {
		return ErrUnknownRPC
	}
	if !caller.Has(p) {
		return fmt.Errorf("%w: missing %s", apperr.ErrPermissionDenied, p)
	}
	return nil
}

func CheckRoleWrite(caller Grant, added []Permission) error {
	if !caller.CoversAll(added) {
		return ErrRoleGrantsMore
	}
	return nil
}

func CheckAccountRoleAssign(caller, role, target Grant) error {
	if !caller.Covers(role) {
		return ErrRoleGrantsMore
	}
	return CheckAccountRoleUnassign(caller, target)
}

func CheckAccountRoleUnassign(caller, target Grant) error {
	if !caller.Covers(target) {
		return ErrTargetHoldsMore
	}
	return nil
}

func CheckGroupRoleAssign(caller, role Grant) error {
	if !caller.Covers(role) {
		return ErrRoleGrantsMore
	}
	return nil
}

func CheckGroupRoleUnassign(caller Grant, roleIsTenantAdmin bool) error {
	return requireTenantAdminFor(caller, roleIsTenantAdmin)
}

func CheckAccountGroups(caller, target, newGroups Grant) error {
	if !caller.Covers(target) {
		return ErrTargetHoldsMore
	}
	if !caller.Covers(newGroups) {
		return ErrGroupsGrantMore
	}
	return nil
}

func CheckGroupCreate(caller, parentChain Grant) error {
	if !caller.Covers(parentChain) {
		return ErrParentGrantsMore
	}
	return nil
}

func CheckGroupMove(caller, oldChain, newChain Grant) error {
	if err := CheckGroupDelete(caller, oldChain); err != nil {
		return err
	}
	return CheckGroupCreate(caller, newChain)
}

func CheckGroupDelete(caller, chain Grant) error {
	return requireTenantAdminFor(caller, chain.TenantAdmin())
}

func requireTenantAdminFor(caller Grant, touchesTenantAdmin bool) error {
	if touchesTenantAdmin && !caller.TenantAdmin() {
		return ErrTenantAdminOnly
	}
	return nil
}
