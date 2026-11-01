package store

import (
	"context"
	"time"
)

type AdminTxOptions struct {
	Tenant   string
	TreeLock bool
	ReadOnly bool
}

type Grant struct {
	TenantAdmin bool
	Permissions []string
}

type GroupNode struct {
	Group
	Ancestors []string
}

type AdminTx interface {
	RunningSession(ctx context.Context, c SessionCheck) error
	AccountGrant(ctx context.Context, tenantID, accountID string) (Grant, error)
	GroupsGrant(ctx context.Context, tenantID string, groupIDs []string) (Grant, error)
	LockAccount(ctx context.Context, tenantID, id string) error
	GroupNode(ctx context.Context, tenantID, id string) (GroupNode, error)
	CreateGroup(ctx context.Context, g Group) error
	UpdateGroup(ctx context.Context, tenantID, id string, update GroupUpdate, at time.Time, by string) error
	DeleteGroup(ctx context.Context, tenantID, id string, at time.Time, by string) (rolesRemoved int64, err error)
	SetAccountGroups(ctx context.Context, tenantID, accountID string, groupIDs []string) error
	Audit(ctx context.Context, a AuditEvent) error
}

type AdminRunner interface {
	RunAdmin(ctx context.Context, opts AdminTxOptions, fn func(AdminTx) error) error
}
