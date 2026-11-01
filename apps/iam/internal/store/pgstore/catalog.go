package pgstore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	errIAMCatalog    = fmt.Errorf("%w: the iam catalog comes from migrations", apperr.ErrFailedPrecondition)
	errClientMissing = fmt.Errorf("%w: client id is required", apperr.ErrInvalidArgument)
)

const missingPermissionsSQL = "SELECT coalesce(array_agg(DISTINCT u.p ORDER BY u.p), '{}') FROM unnest($1::text[]) AS u(p) WHERE NOT EXISTS (SELECT 1 FROM client_permissions cp WHERE cp.permission = u.p)"

const permissionsInUseSQL = `SELECT coalesce(array_agg(DISTINCT rp.permission ORDER BY rp.permission), '{}')
FROM client_permissions cp
JOIN role_permissions rp ON rp.permission = cp.permission
WHERE cp.client_id = $1 AND cp.permission <> ALL($2)`

func requireCataloged(ctx context.Context, tx pgx.Tx, perms []string) error {
	if len(perms) == 0 {
		return nil
	}
	var missing []string
	if err := tx.QueryRow(ctx, missingPermissionsSQL, perms).Scan(&missing); err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: permission not in catalog: %s", apperr.ErrInvalidArgument, strings.Join(missing, ", "))
	}
	return nil
}

func (s *Store) SetClientPermissions(ctx context.Context, clientID string, entries []store.ClientPermission, audit store.AuditEvent) error {
	perms, descriptions, err := catalogEntries(clientID, entries)
	if err != nil {
		return err
	}
	return s.adminWrite(ctx, adminTxOptions{}, audit, func(tx pgx.Tx) error {
		return setClientPermissionsTx(ctx, tx, clientID, perms, descriptions, audit.At, audit.Actor)
	})
}

func catalogEntries(clientID string, entries []store.ClientPermission) (perms, descriptions []string, err error) {
	switch clientID {
	case "":
		return nil, nil, errClientMissing
	case authz.IAMClient:
		return nil, nil, errIAMCatalog
	}
	perms = make([]string, 0, len(entries))
	descriptions = make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Permission, clientID+":") {
			return nil, nil, fmt.Errorf("%w: permission %s does not belong to client %s", apperr.ErrInvalidArgument, e.Permission, clientID)
		}
		if slices.Contains(perms, e.Permission) {
			return nil, nil, fmt.Errorf("%w: permission %s listed twice", apperr.ErrInvalidArgument, e.Permission)
		}
		perms = append(perms, e.Permission)
		descriptions = append(descriptions, e.Description)
	}
	return perms, descriptions, nil
}

func setClientPermissionsTx(ctx context.Context, tx pgx.Tx, clientID string, perms, descriptions []string, at time.Time, by string) error {
	_, err := tx.Exec(ctx,
		"INSERT INTO clients (id, name, created_at, updated_at, updated_by) VALUES ($1, $1, $2, $2, $3) ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by",
		clientID, at, by)
	if err != nil {
		return err
	}
	var inUse []string
	if err := tx.QueryRow(ctx, permissionsInUseSQL, clientID, perms).Scan(&inUse); err != nil {
		return err
	}
	if len(inUse) > 0 {
		return fmt.Errorf("%w: permission in use: %s", apperr.ErrFailedPrecondition, strings.Join(inUse, ", "))
	}
	if _, err := tx.Exec(ctx, "DELETE FROM client_permissions WHERE client_id = $1 AND permission <> ALL($2)", clientID, perms); err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		"INSERT INTO client_permissions (permission, client_id, description) SELECT u.p, $1, u.d FROM unnest($2::text[], $3::text[]) AS u(p, d) ON CONFLICT (permission) DO UPDATE SET description = EXCLUDED.description",
		clientID, perms, descriptions)
	return err
}
