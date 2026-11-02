package pgstore

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const slotUnlockTimeout = 2 * time.Second

const changesSQL = `WITH horizon AS (
	SELECT pg_snapshot_xmin(pg_current_snapshot()) AS xmin
),
page AS (
	SELECT c.change_xid, c.kind, c.tenant_id, c.id, c.ver
	FROM directory_changes c, horizon h
	WHERE (c.change_xid, c.kind, c.tenant_id, c.id) > ($1::text::xid8, $2::smallint, $3::text, $4::text)
		AND c.change_xid < h.xmin
	ORDER BY c.change_xid, c.kind, c.tenant_id, c.id
	LIMIT $5
),
pending AS (
	SELECT EXISTS (
		SELECT 1 FROM directory_changes c, horizon h
		WHERE c.change_xid >= h.xmin
			AND (c.change_xid, c.kind, c.tenant_id, c.id) > ($1::text::xid8, $2::smallint, $3::text, $4::text)
	) AS above
)
SELECT p.change_xid::text, p.kind, p.tenant_id, p.id, p.ver,
	u.id IS NOT NULL, u.state, u.display_name, u.avatar_url,
	CASE WHEN u.id IS NOT NULL THEN array(
		SELECT ug.group_id FROM user_groups ug
		WHERE ug.tenant_id = p.tenant_id AND ug.user_id = p.id
		ORDER BY ug.group_id
	) END,
	g.id IS NOT NULL, g.name, g.parent_id, g.mentionable, g.state,
	pending.above
FROM pending
LEFT JOIN page p ON true
LEFT JOIN users u ON p.kind = 1 AND u.tenant_id = p.tenant_id AND u.id = p.id
LEFT JOIN groups g ON p.kind = 2 AND g.tenant_id = p.tenant_id AND g.id = p.id
ORDER BY p.change_xid, p.kind, p.tenant_id, p.id`

const loadPageSQL = `SELECT u.tenant_id, u.id, u.display_name, u.avatar_url, u.state, c.ver
FROM (
	SELECT tenant_id, id, display_name, avatar_url, state
	FROM users
	WHERE (tenant_id, id) > ($1, $2)
	ORDER BY tenant_id, id
	LIMIT $3
) u
CROSS JOIN LATERAL (
	SELECT ver FROM directory_changes
	WHERE kind = 1 AND tenant_id = u.tenant_id AND id = u.id
	LIMIT 1
) c
ORDER BY u.tenant_id, u.id`

var errBadLimit = fmt.Errorf("%w: limit must be positive", apperr.ErrInvalidArgument)

type changeRow struct {
	xid                    *string
	kind                   *store.ChangeKind
	tenant, id             *string
	ver                    *int64
	hasUser                bool
	userState              *store.State
	displayName, avatarURL *string
	groups                 []string
	hasGroup               bool
	groupName, groupParent *string
	groupMentionable       *bool
	groupState             *store.GroupState
	pendingAboveHorizon    bool
}

func (s *Store) Changes(ctx context.Context, after store.Position, limit int) (store.ChangePage, error) {
	if limit <= 0 {
		return store.ChangePage{}, errBadLimit
	}
	rows, err := s.pool.Query(ctx, changesSQL,
		strconv.FormatUint(after.Xid, 10), after.Kind, after.Tenant, after.ID, limit)
	if err != nil {
		return store.ChangePage{}, s.mapErr(err)
	}
	defer rows.Close()
	page := store.ChangePage{Next: after}
	for rows.Next() {
		var r changeRow
		err := rows.Scan(&r.xid, &r.kind, &r.tenant, &r.id, &r.ver,
			&r.hasUser, &r.userState, &r.displayName, &r.avatarURL, &r.groups,
			&r.hasGroup, &r.groupName, &r.groupParent, &r.groupMentionable, &r.groupState,
			&r.pendingAboveHorizon)
		if err != nil {
			return store.ChangePage{}, s.mapErr(err)
		}
		page.PendingAboveHorizon = r.pendingAboveHorizon
		if r.xid == nil {
			continue
		}
		xid, err := strconv.ParseUint(*r.xid, 10, 64)
		if err != nil {
			return store.ChangePage{}, s.mapErr(fmt.Errorf("parse change_xid %q: %w", *r.xid, err))
		}
		page.Changes = append(page.Changes, r.change())
		page.Next = store.Position{Xid: xid, Kind: *r.kind, Tenant: *r.tenant, ID: *r.id}
	}
	if err := rows.Err(); err != nil {
		return store.ChangePage{}, s.mapErr(err)
	}
	return page, nil
}

func (r changeRow) change() store.Change {
	c := store.Change{Kind: *r.kind, TenantID: *r.tenant, ID: *r.id, Ver: *r.ver}
	if r.hasUser {
		groups := r.groups
		if groups == nil {
			groups = []string{}
		}
		c.User = &store.UserChange{State: *r.userState, DisplayName: *r.displayName, AvatarURL: *r.avatarURL, Groups: groups}
	}
	if r.hasGroup {
		c.Group = &store.GroupChange{Name: *r.groupName, ParentID: deref(r.groupParent), Mentionable: *r.groupMentionable, State: *r.groupState}
	}
	return c
}

func (s *Store) LoadStart(ctx context.Context) (store.Position, error) {
	var xmin string
	if err := s.pool.QueryRow(ctx, "SELECT pg_snapshot_xmin(pg_current_snapshot())::text").Scan(&xmin); err != nil {
		return store.Position{}, s.mapErr(err)
	}
	xid, err := strconv.ParseUint(xmin, 10, 64)
	if err != nil {
		return store.Position{}, s.mapErr(fmt.Errorf("parse snapshot xmin %q: %w", xmin, err))
	}
	return store.Position{Xid: xid}, nil
}

func (s *Store) LoadPage(ctx context.Context, after store.UserKey, limit int) ([]store.DirectoryEntry, error) {
	if limit <= 0 {
		return nil, errBadLimit
	}
	rows, err := s.pool.Query(ctx, loadPageSQL, after.TenantID, after.ID, limit)
	if err != nil {
		return nil, s.mapErr(err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.DirectoryEntry, error) {
		var e store.DirectoryEntry
		err := row.Scan(&e.TenantID, &e.ID, &e.DisplayName, &e.AvatarURL, &e.State, &e.Ver)
		return e, err
	})
	return entries, s.mapErr(err)
}

func (s *Store) TryLoadSlot(ctx context.Context, slots int) (func(), bool, error) {
	if slots <= 0 {
		return nil, false, fmt.Errorf("%w: slots must be positive", apperr.ErrInvalidArgument)
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, s.mapErr(err)
	}
	for slot := range int32(min(slots, 1<<31-1)) {
		var held bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(1, $1::int4)", slot).Scan(&held); err != nil {
			destroy(conn)
			return nil, false, s.mapErr(err)
		}
		if held {
			return slotRelease(conn, slot), true, nil
		}
	}
	conn.Release()
	return nil, false, nil
}

func slotRelease(conn *pgxpool.Conn, slot int32) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), slotUnlockTimeout)
			defer cancel()
			var unlocked bool
			err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock(1, $1::int4)", slot).Scan(&unlocked)
			if err != nil || !unlocked {
				destroy(conn)
				return
			}
			conn.Release()
		})
	}
}

func destroy(conn *pgxpool.Conn) {
	raw := conn.Hijack()
	ctx, cancel := context.WithTimeout(context.Background(), slotUnlockTimeout)
	defer cancel()
	_ = raw.Close(ctx)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
