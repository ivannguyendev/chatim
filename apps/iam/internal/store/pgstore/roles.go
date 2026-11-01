package pgstore

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errBuiltinRole = fmt.Errorf("%w: builtin role cannot change", apperr.ErrFailedPrecondition)

const roleSelectSQL = "SELECT r.tenant_id, r.id, r.name, r.builtin, r.ver, ARRAY(SELECT rp.permission FROM role_permissions rp WHERE rp.tenant_id = r.tenant_id AND rp.role_id = r.id ORDER BY rp.permission), r.created_at, r.updated_at, r.updated_by FROM roles r "

func (s *Store) CreateRole(ctx context.Context, r store.Role, audit store.AuditEvent) error {
	return s.adminWrite(ctx, adminTxOptions{}, audit, func(tx pgx.Tx) error {
		return createRoleTx(ctx, tx, r)
	})
}

func (s *Store) UpdateRole(ctx context.Context, tenantID, id string, u store.RoleUpdate, audit store.AuditEvent) error {
	return s.adminWrite(ctx, adminTxOptions{}, audit, func(tx pgx.Tx) error {
		_, _, err := updateRoleTx(ctx, tx, tenantID, id, u, audit.At, audit.Actor)
		return err
	})
}

func (s *Store) DeleteRole(ctx context.Context, tenantID, id string, audit store.AuditEvent) (store.RoleRemoval, error) {
	var removed store.RoleRemoval
	err := s.adminTx(ctx, adminTxOptions{}, func(tx pgx.Tx) error {
		var err error
		if removed, err = deleteRoleTx(ctx, tx, tenantID, id); err != nil {
			return err
		}
		audit = withAuditDetail(audit, "accounts", strconv.Itoa(removed.Accounts))
		return insertAudit(ctx, tx, withAuditDetail(audit, "groups", strconv.Itoa(removed.Groups)))
	})
	if err != nil {
		return store.RoleRemoval{}, err
	}
	return removed, nil
}

func (s *Store) ListRoles(ctx context.Context, tenantID string) ([]store.Role, error) {
	var roles []store.Role
	err := s.adminTx(ctx, adminTxOptions{readOnly: true}, func(tx pgx.Tx) error {
		var err error
		roles, err = queryRoles(ctx, tx, roleSelectSQL+"WHERE r.tenant_id = $1 ORDER BY r.id", tenantID)
		return err
	})
	return roles, err
}

func (a *adminTxView) CreateRole(ctx context.Context, r store.Role) error {
	return a.s.mapErr(createRoleTx(ctx, a.tx, r))
}

func (a *adminTxView) UpdateRole(ctx context.Context, tenantID, id string, u store.RoleUpdate, at time.Time, by string) (store.Role, []string, error) {
	r, added, err := updateRoleTx(ctx, a.tx, tenantID, id, u, at, by)
	if err != nil {
		return store.Role{}, nil, a.s.mapErr(err)
	}
	return r, added, nil
}

func (a *adminTxView) DeleteRole(ctx context.Context, tenantID, id string) (store.RoleRemoval, error) {
	removed, err := deleteRoleTx(ctx, a.tx, tenantID, id)
	if err != nil {
		return store.RoleRemoval{}, a.s.mapErr(err)
	}
	return removed, nil
}

func (a *adminTxView) Role(ctx context.Context, tenantID, id string) (store.Role, error) {
	r, err := readRole(ctx, a.tx, tenantID, id)
	return r, a.s.mapErr(err)
}

func (a *adminTxView) RoleGrant(ctx context.Context, tenantID, id string) (store.Grant, error) {
	r, err := a.Role(ctx, tenantID, id)
	if err != nil {
		return store.Grant{}, err
	}
	return store.Grant{TenantAdmin: r.Builtin && r.ID == authz.TenantAdminRole, Permissions: r.Permissions}, nil
}

func createRoleTx(ctx context.Context, tx pgx.Tx, r store.Role) error {
	perms := orEmpty(r.Permissions)
	if err := requireCataloged(ctx, tx, perms); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		"INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $3, false, 1, $4, $5, $6)",
		r.TenantID, r.ID, r.Name, r.CreatedAt, r.UpdatedAt, r.UpdatedBy)
	if err != nil {
		return err
	}
	_, err = insertRolePermissions(ctx, tx, r.TenantID, r.ID, perms)
	return err
}

func updateRoleTx(ctx context.Context, tx pgx.Tx, tenantID, id string, u store.RoleUpdate, at time.Time, by string) (store.Role, []string, error) {
	if err := lockEditableRole(ctx, tx, tenantID, id); err != nil {
		return store.Role{}, nil, err
	}
	added := []string{}
	if u.Permissions != nil {
		perms := orEmpty(*u.Permissions)
		if err := requireCataloged(ctx, tx, perms); err != nil {
			return store.Role{}, nil, err
		}
		_, err := tx.Exec(ctx, "DELETE FROM role_permissions WHERE tenant_id = $1 AND role_id = $2 AND permission <> ALL($3)", tenantID, id, perms)
		if err != nil {
			return store.Role{}, nil, err
		}
		if added, err = insertRolePermissions(ctx, tx, tenantID, id, perms); err != nil {
			return store.Role{}, nil, err
		}
	}
	_, err := tx.Exec(ctx,
		"UPDATE roles SET name = coalesce($3, name), ver = ver + 1, updated_at = $4, updated_by = $5 WHERE tenant_id = $1 AND id = $2",
		tenantID, id, u.Name, at, by)
	if err != nil {
		return store.Role{}, nil, err
	}
	r, err := readRole(ctx, tx, tenantID, id)
	return r, added, err
}

func deleteRoleTx(ctx context.Context, tx pgx.Tx, tenantID, id string) (store.RoleRemoval, error) {
	if err := lockEditableRole(ctx, tx, tenantID, id); err != nil {
		return store.RoleRemoval{}, err
	}
	accounts, err := tx.Exec(ctx, "DELETE FROM account_roles WHERE tenant_id = $1 AND role_id = $2", tenantID, id)
	if err != nil {
		return store.RoleRemoval{}, err
	}
	groups, err := tx.Exec(ctx, "DELETE FROM group_roles WHERE tenant_id = $1 AND role_id = $2", tenantID, id)
	if err != nil {
		return store.RoleRemoval{}, err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM role_permissions WHERE tenant_id = $1 AND role_id = $2", tenantID, id); err != nil {
		return store.RoleRemoval{}, err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM roles WHERE tenant_id = $1 AND id = $2", tenantID, id); err != nil {
		return store.RoleRemoval{}, err
	}
	return store.RoleRemoval{Accounts: int(accounts.RowsAffected()), Groups: int(groups.RowsAffected())}, nil
}

func lockEditableRole(ctx context.Context, tx pgx.Tx, tenantID, id string) error {
	var builtin bool
	if err := tx.QueryRow(ctx, "SELECT builtin FROM roles WHERE tenant_id = $1 AND id = $2 FOR UPDATE", tenantID, id).Scan(&builtin); err != nil {
		return err
	}
	if builtin {
		return errBuiltinRole
	}
	return nil
}

func insertRolePermissions(ctx context.Context, tx pgx.Tx, tenantID, id string, perms []string) ([]string, error) {
	rows, err := tx.Query(ctx,
		"INSERT INTO role_permissions (tenant_id, role_id, permission) SELECT $1, $2, unnest($3::text[]) ON CONFLICT DO NOTHING RETURNING permission",
		tenantID, id, perms)
	if err != nil {
		return nil, err
	}
	added, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	slices.Sort(added)
	return added, nil
}

func readRole(ctx context.Context, tx pgx.Tx, tenantID, id string) (store.Role, error) {
	rows, err := tx.Query(ctx, roleSelectSQL+"WHERE r.tenant_id = $1 AND r.id = $2", tenantID, id)
	if err != nil {
		return store.Role{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanRole)
}

func queryRoles(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]store.Role, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanRole)
}

func scanRole(row pgx.CollectableRow) (store.Role, error) {
	var r store.Role
	err := row.Scan(&r.TenantID, &r.ID, &r.Name, &r.Builtin, &r.Ver, &r.Permissions, &r.CreatedAt, &r.UpdatedAt, &r.UpdatedBy)
	return r, err
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
