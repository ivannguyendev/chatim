package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	runningSessionWhere = "s.id = $1 AND s.tenant_id = $2 AND s.account_id = $3 AND s.state = 1 AND s.idle_expires_at > $4 AND s.expires_at > $4"
	profileColumns      = "id, username, coalesce(email, ''), display_name, avatar_url"
)

func (s *Store) MyProfile(ctx context.Context, c store.SessionCheck) (store.Profile, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT a.id, a.username, coalesce(a.email, ''), a.display_name, a.avatar_url FROM sessions s JOIN accounts a ON a.tenant_id = s.tenant_id AND a.id = s.account_id WHERE "+runningSessionWhere,
		c.SessionID, c.TenantID, c.AccountID, c.Now)
	p, err := scanProfile(row)
	return p, s.mapErr(err)
}

func (s *Store) UpdateMyProfile(ctx context.Context, c store.SessionCheck, u store.ProfileUpdate, audit store.AuditEvent) (store.Profile, error) {
	var p store.Profile
	err := s.write(ctx, audit, func(tx pgx.Tx) error {
		var running int
		if err := tx.QueryRow(ctx, "SELECT 1 FROM sessions s WHERE "+runningSessionWhere+" FOR SHARE",
			c.SessionID, c.TenantID, c.AccountID, c.Now).Scan(&running); err != nil {
			return err
		}
		row := tx.QueryRow(ctx,
			"UPDATE accounts SET display_name = coalesce($3, display_name), avatar_url = coalesce($4, avatar_url), updated_at = $5, updated_by = $6 WHERE tenant_id = $1 AND id = $2 RETURNING "+profileColumns,
			c.TenantID, c.AccountID, u.DisplayName, u.AvatarURL, audit.At, audit.Actor)
		var err error
		p, err = scanProfile(row)
		return err
	})
	if err != nil {
		return store.Profile{}, err
	}
	return p, nil
}

func scanProfile(row pgx.Row) (store.Profile, error) {
	var p store.Profile
	err := row.Scan(&p.ID, &p.Username, &p.Email, &p.DisplayName, &p.AvatarURL)
	return p, err
}
