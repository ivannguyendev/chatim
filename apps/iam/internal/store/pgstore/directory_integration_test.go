package pgstore_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	userKind  = 1
	groupKind = 2
	minhID    = "a2aaaaaaaaaaaaaaaaaaaaaaaa"
	anID      = "a3aaaaaaaaaaaaaaaaaaaaaaaa"
)

func connect(t *testing.T, uri string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), uri)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func exec(t *testing.T, conn execer, sql string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func changeVer(t *testing.T, s fixture, kind int, tenant, id string) int64 {
	t.Helper()
	var ver int64
	err := s.raw.QueryRow(context.Background(),
		"SELECT ver FROM directory_changes WHERE kind = $1 AND tenant_id = $2 AND id = $3", kind, tenant, id).Scan(&ver)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read directory_changes: %v", err)
	}
	return ver
}

func wantVer(t *testing.T, s fixture, kind int, id string, want int64, step string) {
	t.Helper()
	if got := changeVer(t, s, kind, "shop", id); got != want {
		t.Fatalf("%s: ver of %d/%s = %d; want %d", step, kind, id, got, want)
	}
}

func insertGroup(t *testing.T, s fixture, id, name string, parent any) {
	t.Helper()
	exec(t, s.raw, "INSERT INTO groups (tenant_id, id, name, parent_id, state, created_at, updated_at, updated_by) VALUES ('shop', $1, $2, $3, 1, now(), now(), 'test')", id, name, parent)
}

func listen(t *testing.T, uri string) *pgx.Conn {
	t.Helper()
	conn := connect(t, uri)
	exec(t, conn, "LISTEN identity_changes")
	return conn
}

func notificationsUntil(t *testing.T, listener, marker *pgx.Conn, mark string) []string {
	t.Helper()
	exec(t, marker, "SELECT pg_notify('identity_changes', $1)", mark)
	var got []string
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := listener.WaitForNotification(ctx)
		cancel()
		if err != nil {
			t.Fatalf("wait for %q after %v: %v", mark, got, err)
		}
		if n.Payload == mark {
			return got
		}
		got = append(got, n.Payload)
	}
}

func wantNotifications(t *testing.T, listener, marker *pgx.Conn, mark string, want ...string) {
	t.Helper()
	if got := notificationsUntil(t, listener, marker, mark); !slices.Equal(got, want) {
		t.Fatalf("%s: notifications = %v; want %v", mark, got, want)
	}
}

func TestProfileUpdateBumpsVerOnlyWhenAFieldChanges(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", lanID, "lan")
	wantVer(t, s, userKind, lanID, 1, "create")
	var displayName string
	if err := s.raw.QueryRow(ctx, "SELECT display_name FROM users WHERE id = $1", lanID).Scan(&displayName); err != nil || displayName != "lan" {
		t.Fatalf("display_name = %q, %v; want lan", displayName, err)
	}
	exec(t, s.raw, "UPDATE users SET display_name = 'Lan Nguyen' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 2, "display name changed")
	exec(t, s.raw, "UPDATE users SET display_name = 'Lan Nguyen' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 2, "same display name")
	exec(t, s.raw, "UPDATE users SET updated_by = 'someone' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 2, "no directory field changed")
	exec(t, s.raw, "UPDATE users SET avatar_url = 'https://cdn.example/lan.png' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 3, "avatar changed")
	if err := s.SetUserState(ctx, "shop", lanID, store.StateDisabled, audit("user_disable")); err != nil {
		t.Fatalf("SetUserState: %v", err)
	}
	wantVer(t, s, userKind, lanID, 4, "state changed")
}

func TestUserGroupsChangeBumpsTheUser(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", lanID, "lan")
	insertGroup(t, s, "sales", "Sales", nil)
	exec(t, s.raw, "INSERT INTO user_groups (tenant_id, user_id, group_id) VALUES ('shop', $1, 'sales')", lanID)
	wantVer(t, s, userKind, lanID, 2, "joined")
	wantVer(t, s, groupKind, "sales", 1, "group after join")
	exec(t, s.raw, "DELETE FROM user_groups WHERE tenant_id = 'shop' AND user_id = $1", lanID)
	wantVer(t, s, userKind, lanID, 3, "left")
}

func TestGroupUpdateBumpsTheGroup(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	insertGroup(t, s, "sales", "Sales", nil)
	insertGroup(t, s, "sales-hn", "Sales HN", "sales")
	wantVer(t, s, groupKind, "sales", 1, "create")
	wantVer(t, s, groupKind, "sales-hn", 1, "create child")
	exec(t, s.raw, "UPDATE groups SET name = 'Sales VN' WHERE id = 'sales'")
	wantVer(t, s, groupKind, "sales", 2, "renamed")
	exec(t, s.raw, "UPDATE groups SET name = 'Sales VN' WHERE id = 'sales'")
	wantVer(t, s, groupKind, "sales", 2, "same name")
	exec(t, s.raw, "UPDATE groups SET mentionable = false WHERE id = 'sales'")
	wantVer(t, s, groupKind, "sales", 3, "mentionable")
	exec(t, s.raw, "UPDATE groups SET parent_id = NULL WHERE id = 'sales-hn'")
	wantVer(t, s, groupKind, "sales-hn", 2, "moved")
	exec(t, s.raw, "UPDATE groups SET state = 2 WHERE id = 'sales-hn'")
	wantVer(t, s, groupKind, "sales-hn", 3, "deleted")
	wantVer(t, s, groupKind, "sales", 3, "parent after child changes")
}

func TestNotifyIsSentAfterCommitOnly(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", lanID, "lan")
	seedUser(t, s, "shop", minhID, "minh")
	listener := listen(t, s.uri)
	marker := connect(t, s.uri)
	tx, err := s.raw.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	exec(t, tx, "UPDATE users SET display_name = 'Lan Nguyen' WHERE id = $1", lanID)
	exec(t, tx, "UPDATE users SET display_name = 'Minh Tran' WHERE id = $1", minhID)
	wantNotifications(t, listener, marker, "before-commit")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	wantNotifications(t, listener, marker, "after-commit", "dir")
	tx, err = s.raw.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	exec(t, tx, "UPDATE users SET display_name = 'Lan' WHERE id = $1", lanID)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	wantNotifications(t, listener, marker, "after-rollback")
}

func TestStateChangesNotifyTheirChannel(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", lanID, "lan")
	listener := listen(t, s.uri)
	marker := connect(t, s.uri)
	if err := s.SetUserState(ctx, "shop", lanID, store.StateDisabled, audit("user_disable")); err != nil {
		t.Fatalf("SetUserState lan: %v", err)
	}
	wantNotifications(t, listener, marker, "user", "dir")
	if err := s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable")); err != nil {
		t.Fatalf("SetTenantState: %v", err)
	}
	wantNotifications(t, listener, marker, "tenant", "rev")
	exec(t, s.raw, "UPDATE tenants SET name = 'Shop' WHERE id = 'shop'")
	wantNotifications(t, listener, marker, "tenant-rename")
}

func userVer(t *testing.T, s fixture, id string) int64 {
	t.Helper()
	var ver int64
	if err := s.raw.QueryRow(context.Background(), "SELECT ver FROM users WHERE tenant_id = 'shop' AND id = $1", id).Scan(&ver); err != nil {
		t.Fatalf("read users.ver: %v", err)
	}
	return ver
}

func TestUserSetBumpsUserVerEveryTimeAndTheFeedOnlyOnChange(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	listener := listen(t, s.uri)
	marker := connect(t, s.uri)
	seedUser(t, s, "shop", lanID, "lan")
	wantVer(t, s, userKind, lanID, 1, "insert")
	wantNotifications(t, listener, marker, "insert", "dir")
	if err := s.SetProfile(ctx, "shop", lanID, store.ProfileUpdate{DisplayName: new("lan")}, audit("user_set")); err != nil {
		t.Fatalf("SetProfile same name: %v", err)
	}
	wantVer(t, s, userKind, lanID, 1, "same name")
	if got := userVer(t, s, lanID); got != 2 {
		t.Fatalf("users.ver after same name = %d; want 2", got)
	}
	wantNotifications(t, listener, marker, "same-name")
	if err := s.SetProfile(ctx, "shop", lanID, store.ProfileUpdate{DisplayName: new("Lan Nguyen")}, audit("user_set")); err != nil {
		t.Fatalf("SetProfile new name: %v", err)
	}
	wantVer(t, s, userKind, lanID, 2, "new name")
	if got := userVer(t, s, lanID); got != 3 {
		t.Fatalf("users.ver after new name = %d; want 3", got)
	}
	wantNotifications(t, listener, marker, "new-name", "dir")
}
