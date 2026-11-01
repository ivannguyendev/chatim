package pgstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const accountDirectSQL = `WITH direct AS (
	SELECT g.id, g.ancestors
	FROM account_groups ag
	JOIN groups g ON g.tenant_id = ag.tenant_id AND g.id = ag.group_id
	WHERE ag.tenant_id = $1 AND ag.account_id = $2 AND g.state = $3
),
`

const groupsDirectSQL = `WITH direct AS (
	SELECT g.id, g.ancestors
	FROM groups g
	WHERE g.tenant_id = $1 AND g.id = ANY($2::text[]) AND g.state = $3
),
`

const reachSQL = `reach AS (
	SELECT id FROM direct
	UNION
	SELECT unnest(ancestors) FROM direct
),
`

const groupHeldSQL = `held AS (
	SELECT gr.role_id
	FROM reach r
	JOIN group_roles gr ON gr.tenant_id = $1 AND gr.group_id = r.id
`

const accountHeldSQL = `	UNION
	SELECT ar.role_id FROM account_roles ar WHERE ar.tenant_id = $1 AND ar.account_id = $2
`

const grantSelectSQL = `)
SELECT coalesce(bool_or(ro.builtin AND ro.id = 'tenant-admin'), false),
	coalesce(array_agg(DISTINCT rp.permission ORDER BY rp.permission) FILTER (WHERE rp.permission IS NOT NULL), '{}')
FROM held h
JOIN roles ro ON ro.tenant_id = $1 AND ro.id = h.role_id
LEFT JOIN role_permissions rp ON rp.tenant_id = ro.tenant_id AND rp.role_id = ro.id`

const (
	accountGrantSQL = accountDirectSQL + reachSQL + groupHeldSQL + accountHeldSQL + grantSelectSQL
	groupsGrantSQL  = groupsDirectSQL + reachSQL + groupHeldSQL + grantSelectSQL
)

func (a *adminTxView) AccountGrant(ctx context.Context, tenantID, accountID string) (store.Grant, error) {
	return a.grant(ctx, accountGrantSQL, tenantID, accountID)
}

func (a *adminTxView) GroupsGrant(ctx context.Context, tenantID string, groupIDs []string) (store.Grant, error) {
	if groupIDs == nil {
		groupIDs = []string{}
	}
	return a.grant(ctx, groupsGrantSQL, tenantID, groupIDs)
}

func (a *adminTxView) grant(ctx context.Context, sql, tenantID string, subject any) (store.Grant, error) {
	var g store.Grant
	err := a.tx.QueryRow(ctx, sql, tenantID, subject, store.GroupLive).Scan(&g.TenantAdmin, &g.Permissions)
	if err != nil {
		return store.Grant{}, a.s.mapErr(err)
	}
	return g, nil
}
