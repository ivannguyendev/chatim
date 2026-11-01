package pgstore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type roleBinding struct {
	addSQL    string
	removeSQL string
	pruneSQL  string
	fillSQL   string
}

func newRoleBinding(table, subject string) roleBinding {
	columns := " (tenant_id, " + subject + ", role_id, created_at, created_by) "
	return roleBinding{
		addSQL:    "INSERT INTO " + table + columns + "VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING",
		removeSQL: "DELETE FROM " + table + " WHERE tenant_id = $1 AND " + subject + " = $2 AND role_id = $3",
		pruneSQL:  "DELETE FROM " + table + " WHERE tenant_id = $1 AND " + subject + " = $2 AND role_id <> ALL($3)",
		fillSQL:   "INSERT INTO " + table + columns + "SELECT $1, $2, unnest($3::text[]), $4, $5 ON CONFLICT DO NOTHING",
	}
}

var (
	accountBinding = newRoleBinding("account_roles", "account_id")
	groupBinding   = newRoleBinding("group_roles", "group_id")
)

func (b roleBinding) add(ctx context.Context, tx pgx.Tx, tenantID, subject, roleID string, at time.Time, by string) (bool, error) {
	tag, err := tx.Exec(ctx, b.addSQL, tenantID, subject, roleID, at, by)
	return err == nil && tag.RowsAffected() == 1, err
}

func (b roleBinding) remove(ctx context.Context, tx pgx.Tx, tenantID, subject, roleID string) (bool, error) {
	tag, err := tx.Exec(ctx, b.removeSQL, tenantID, subject, roleID)
	return err == nil && tag.RowsAffected() == 1, err
}

func (b roleBinding) replace(ctx context.Context, tx pgx.Tx, tenantID, subject string, roleIDs []string, at time.Time, by string) error {
	if _, err := tx.Exec(ctx, b.pruneSQL, tenantID, subject, roleIDs); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, b.fillSQL, tenantID, subject, roleIDs, at, by)
	return err
}

func (a *adminTxView) AssignAccountRole(ctx context.Context, tenantID, accountID, roleID string, at time.Time, by string) (bool, error) {
	added, err := accountBinding.add(ctx, a.tx, tenantID, accountID, roleID, at, by)
	return added, a.s.mapErr(err)
}

func (a *adminTxView) UnassignAccountRole(ctx context.Context, tenantID, accountID, roleID string) (bool, error) {
	removed, err := accountBinding.remove(ctx, a.tx, tenantID, accountID, roleID)
	return removed, a.s.mapErr(err)
}

func (a *adminTxView) AssignGroupRole(ctx context.Context, tenantID, groupID, roleID string, at time.Time, by string) (bool, error) {
	if err := shareLiveGroup(ctx, a.tx, tenantID, groupID); err != nil {
		return false, a.s.mapErr(err)
	}
	added, err := groupBinding.add(ctx, a.tx, tenantID, groupID, roleID, at, by)
	return added, a.s.mapErr(err)
}

func (a *adminTxView) UnassignGroupRole(ctx context.Context, tenantID, groupID, roleID string) (bool, error) {
	if err := shareLiveGroup(ctx, a.tx, tenantID, groupID); err != nil {
		return false, a.s.mapErr(err)
	}
	removed, err := groupBinding.remove(ctx, a.tx, tenantID, groupID, roleID)
	return removed, a.s.mapErr(err)
}

func (s *Store) SetAccountRoles(ctx context.Context, tenantID, accountID string, roleIDs []string, audit store.AuditEvent) error {
	roles, err := sortedUniqueRoles(roleIDs)
	if err != nil {
		return err
	}
	return s.adminWrite(ctx, adminTxOptions{}, audit, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, "SELECT 1 FROM accounts WHERE tenant_id = $1 AND id = $2 FOR UPDATE", tenantID, accountID).Scan(&one); err != nil {
			return err
		}
		return accountBinding.replace(ctx, tx, tenantID, accountID, roles, audit.At, audit.Actor)
	})
}

func (s *Store) SetGroupRoles(ctx context.Context, tenantID, groupID string, roleIDs []string, audit store.AuditEvent) error {
	roles, err := sortedUniqueRoles(roleIDs)
	if err != nil {
		return err
	}
	return s.adminWrite(ctx, adminTxOptions{}, audit, func(tx pgx.Tx) error {
		if err := shareLiveGroup(ctx, tx, tenantID, groupID); err != nil {
			return err
		}
		return groupBinding.replace(ctx, tx, tenantID, groupID, roles, audit.At, audit.Actor)
	})
}

func (s *Store) CreateAccountWithRole(ctx context.Context, a store.Account, roleID string, audit store.AuditEvent) error {
	return s.adminTx(ctx, adminTxOptions{}, func(tx pgx.Tx) error {
		if err := insertAccountTx(ctx, tx, a); err != nil {
			return err
		}
		if _, err := accountBinding.add(ctx, tx, a.TenantID, a.ID, roleID, audit.At, audit.Actor); err != nil {
			return err
		}
		created := audit
		created.Action = "account_create"
		if err := insertAudit(ctx, tx, created); err != nil {
			return err
		}
		assigned := withAuditDetail(audit, "role", roleID)
		assigned.Action = "account_role_assign"
		return insertAudit(ctx, tx, assigned)
	})
}

func shareLiveGroup(ctx context.Context, tx pgx.Tx, tenantID, id string) error {
	return liveGroup(ctx, tx, liveGroupSQL+" FOR SHARE", tenantID, id)
}

func sortedUniqueRoles(ids []string) ([]string, error) {
	sorted := slices.Clone(orEmpty(ids))
	slices.Sort(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return nil, fmt.Errorf("%w: role %s listed twice", apperr.ErrInvalidArgument, sorted[i])
		}
	}
	return sorted, nil
}
