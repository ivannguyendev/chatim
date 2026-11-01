package store

import (
	"context"
	"time"
)

type Role struct {
	TenantID    string
	ID          string
	Name        string
	Builtin     bool
	Ver         int64
	Permissions []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	UpdatedBy   string
}

type RoleUpdate struct {
	Name        *string
	Permissions *[]string
}

type RoleRemoval struct {
	Accounts int
	Groups   int
}

type AccountView struct {
	ID          string
	Username    string
	DisplayName string
	Email       string
	State       State
	GroupIDs    []string
	RoleIDs     []string
	CreatedAt   time.Time
}

type GroupView struct {
	Group
	RoleIDs []string
}

type ClientPermission struct {
	Permission  string
	Description string
}

type RoleTx interface {
	CreateRole(ctx context.Context, r Role) error
	UpdateRole(ctx context.Context, tenantID, id string, update RoleUpdate, at time.Time, by string) (role Role, added []string, err error)
	DeleteRole(ctx context.Context, tenantID, id string) (RoleRemoval, error)
	Role(ctx context.Context, tenantID, id string) (Role, error)
	RoleGrant(ctx context.Context, tenantID, id string) (Grant, error)
	AssignAccountRole(ctx context.Context, tenantID, accountID, roleID string, at time.Time, by string) (added bool, err error)
	UnassignAccountRole(ctx context.Context, tenantID, accountID, roleID string) (removed bool, err error)
	AssignGroupRole(ctx context.Context, tenantID, groupID, roleID string, at time.Time, by string) (added bool, err error)
	UnassignGroupRole(ctx context.Context, tenantID, groupID, roleID string) (removed bool, err error)
	Account(ctx context.Context, tenantID, id string) (AccountView, error)
	Accounts(ctx context.Context, tenantID, after string, limit int) (page []AccountView, next string, err error)
	Roles(ctx context.Context, tenantID, after string, limit int) (page []Role, next string, err error)
	Groups(ctx context.Context, tenantID, after string, limit int) (page []GroupView, next string, err error)
}

type Roles interface {
	CreateRole(ctx context.Context, r Role, audit AuditEvent) error
	UpdateRole(ctx context.Context, tenantID, id string, update RoleUpdate, audit AuditEvent) error
	DeleteRole(ctx context.Context, tenantID, id string, audit AuditEvent) (RoleRemoval, error)
	ListRoles(ctx context.Context, tenantID string) ([]Role, error)
	SetAccountRoles(ctx context.Context, tenantID, accountID string, roleIDs []string, audit AuditEvent) error
	SetGroupRoles(ctx context.Context, tenantID, groupID string, roleIDs []string, audit AuditEvent) error
	CreateAccountWithRole(ctx context.Context, a Account, roleID string, audit AuditEvent) error
	SetClientPermissions(ctx context.Context, clientID string, entries []ClientPermission, audit AuditEvent) error
}
