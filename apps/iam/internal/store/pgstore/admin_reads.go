package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errPageLimit = fmt.Errorf("%w: page limit must be positive", apperr.ErrInvalidArgument)

const accountViewSQL = `SELECT a.id, a.username, a.display_name, coalesce(a.email, ''), a.state, a.created_at,
	ARRAY(SELECT ag.group_id FROM account_groups ag WHERE ag.tenant_id = a.tenant_id AND ag.account_id = a.id ORDER BY ag.group_id),
	ARRAY(SELECT ar.role_id FROM account_roles ar WHERE ar.tenant_id = a.tenant_id AND ar.account_id = a.id ORDER BY ar.role_id)
FROM accounts a `

const groupViewSQL = "SELECT " + groupColumns + `,
	ARRAY(SELECT gr.role_id FROM group_roles gr WHERE gr.tenant_id = g.tenant_id AND gr.group_id = g.id ORDER BY gr.role_id)
FROM groups g `

func (a *adminTxView) Account(ctx context.Context, tenantID, id string) (store.AccountView, error) {
	rows, err := a.tx.Query(ctx, accountViewSQL+"WHERE a.tenant_id = $1 AND a.id = $2", tenantID, id)
	if err != nil {
		return store.AccountView{}, a.s.mapErr(err)
	}
	v, err := pgx.CollectExactlyOneRow(rows, scanAccountView)
	return v, a.s.mapErr(err)
}

func (a *adminTxView) Accounts(ctx context.Context, tenantID, after string, limit int) ([]store.AccountView, string, error) {
	return readPage(ctx, a, accountViewSQL+"WHERE a.tenant_id = $1 AND a.id > $2 ORDER BY a.id LIMIT $3",
		limit, scanAccountView, func(v store.AccountView) string { return v.ID }, tenantID, after)
}

func (a *adminTxView) Roles(ctx context.Context, tenantID, after string, limit int) ([]store.Role, string, error) {
	return readPage(ctx, a, roleSelectSQL+"WHERE r.tenant_id = $1 AND r.id > $2 ORDER BY r.id LIMIT $3",
		limit, scanRole, func(r store.Role) string { return r.ID }, tenantID, after)
}

func (a *adminTxView) Groups(ctx context.Context, tenantID, after string, limit int) ([]store.GroupView, string, error) {
	return readPage(ctx, a, groupViewSQL+"WHERE g.tenant_id = $1 AND g.id > $2 AND g.state = $3 ORDER BY g.id LIMIT $4",
		limit, scanGroupView, func(v store.GroupView) string { return v.ID }, tenantID, after, store.GroupLive)
}

func readPage[T any](ctx context.Context, a *adminTxView, sql string, limit int, scan pgx.RowToFunc[T], key func(T) string, args ...any) ([]T, string, error) {
	if limit < 1 {
		return nil, "", errPageLimit
	}
	rows, err := a.tx.Query(ctx, sql, append(args, limit+1)...)
	if err != nil {
		return nil, "", a.s.mapErr(err)
	}
	items, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, "", a.s.mapErr(err)
	}
	if len(items) <= limit {
		return items, "", nil
	}
	items = items[:limit]
	return items, key(items[limit-1]), nil
}

func scanAccountView(row pgx.CollectableRow) (store.AccountView, error) {
	var v store.AccountView
	err := row.Scan(&v.ID, &v.Username, &v.DisplayName, &v.Email, &v.State, &v.CreatedAt, &v.GroupIDs, &v.RoleIDs)
	return v, err
}

func scanGroupView(row pgx.CollectableRow) (store.GroupView, error) {
	var v store.GroupView
	err := row.Scan(&v.TenantID, &v.ID, &v.Name, &v.ParentID, &v.Mentionable, &v.State, &v.CreatedAt, &v.UpdatedAt, &v.UpdatedBy, &v.RoleIDs)
	return v, err
}
