package pgstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const insertProfileSQL = `INSERT INTO users (tenant_id, id, display_name, avatar_url, state, ver, created_at, updated_at, updated_by)
	VALUES ($1, $2, $3, $4, $5, 1, $6, $6, $7) ON CONFLICT (tenant_id, id) DO NOTHING`

const updateSeenProfileSQL = `UPDATE users SET display_name = $3, avatar_url = $4, ver = ver + 1, updated_at = $5, updated_by = $6
	WHERE tenant_id = $1 AND id = $2 AND display_name = $7 AND avatar_url = $8`

func (s *Store) WriteProfile(ctx context.Context, w store.ProfileWrite) (bool, error) {
	sql, args := insertProfileSQL, []any{w.TenantID, w.UserID, w.DisplayName, w.AvatarURL, store.StateActive, w.At, w.By}
	if w.Seen != nil {
		sql = updateSeenProfileSQL
		args = []any{w.TenantID, w.UserID, w.DisplayName, w.AvatarURL, w.At, w.By, w.Seen.DisplayName, w.Seen.AvatarURL}
	}
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return false, s.mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}
