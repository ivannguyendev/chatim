package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errTreeLockWithoutTenant = fmt.Errorf("%w: tree lock needs a tenant", apperr.ErrInvalidArgument)

var (
	_ store.AdminRunner = (*Store)(nil)
	_ store.AdminTx     = (*adminTxView)(nil)
)

type adminTxView struct {
	s        *Store
	tx       pgx.Tx
	readOnly bool
}

func (s *Store) RunAdmin(ctx context.Context, opts store.AdminTxOptions, fn func(store.AdminTx) error) error {
	if opts.TreeLock && opts.Tenant == "" {
		return errTreeLockWithoutTenant
	}
	release, err := s.adminSlots.enter(ctx)
	if err != nil {
		return s.mapErr(err)
	}
	defer release()
	inner := treeLockIf(opts.TreeLock, opts.Tenant)
	inner.readOnly = opts.ReadOnly
	return s.adminTx(ctx, inner, func(tx pgx.Tx) error {
		return fn(&adminTxView{s: s, tx: tx, readOnly: opts.ReadOnly})
	})
}

func (a *adminTxView) lock(clause string) string {
	if a.readOnly {
		return ""
	}
	return clause
}

func (a *adminTxView) RunningSession(ctx context.Context, c store.SessionCheck) error {
	var one int
	err := a.tx.QueryRow(ctx, "SELECT 1 FROM sessions s WHERE "+runningSessionWhere+a.lock(" FOR SHARE"),
		c.SessionID, c.TenantID, c.AccountID, c.Now).Scan(&one)
	return a.s.mapErr(err)
}

func (a *adminTxView) LockAccount(ctx context.Context, tenantID, id string) error {
	var one int
	err := a.tx.QueryRow(ctx, "SELECT 1 FROM accounts WHERE tenant_id = $1 AND id = $2"+a.lock(" FOR UPDATE"),
		tenantID, id).Scan(&one)
	return a.s.mapErr(err)
}

func (a *adminTxView) GroupNode(ctx context.Context, tenantID, id string) (store.GroupNode, error) {
	var n store.GroupNode
	err := a.tx.QueryRow(ctx, "SELECT "+groupColumns+", ancestors FROM groups WHERE tenant_id = $1 AND id = $2 AND state = $3"+a.lock(" FOR SHARE"),
		tenantID, id, store.GroupLive).
		Scan(&n.TenantID, &n.ID, &n.Name, &n.ParentID, &n.Mentionable, &n.State, &n.CreatedAt, &n.UpdatedAt, &n.UpdatedBy, &n.Ancestors)
	if err != nil {
		return store.GroupNode{}, a.s.mapErr(err)
	}
	return n, nil
}

func (a *adminTxView) CreateGroup(ctx context.Context, g store.Group) error {
	return a.s.mapErr(createGroupTx(ctx, a.tx, g))
}

func (a *adminTxView) UpdateGroup(ctx context.Context, tenantID, id string, u store.GroupUpdate, at time.Time, by string) error {
	return a.s.mapErr(updateGroupTx(ctx, a.tx, tenantID, id, u, at, by))
}

func (a *adminTxView) DeleteGroup(ctx context.Context, tenantID, id string, at time.Time, by string) (int64, error) {
	roles, err := deleteGroupTx(ctx, a.tx, tenantID, id, at, by)
	return roles, a.s.mapErr(err)
}

func (a *adminTxView) SetAccountGroups(ctx context.Context, tenantID, accountID string, groupIDs []string) error {
	return a.s.mapErr(setAccountGroupsTx(ctx, a.tx, tenantID, accountID, groupIDs))
}

func (a *adminTxView) Audit(ctx context.Context, e store.AuditEvent) error {
	return a.s.mapErr(insertAudit(ctx, a.tx, e))
}
