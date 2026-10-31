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
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
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
	exec(t, conn, "LISTEN iam_changes")
	return conn
}

func notificationsUntil(t *testing.T, listener, marker *pgx.Conn, mark string) []string {
	t.Helper()
	exec(t, marker, "SELECT pg_notify('iam_changes', $1)", mark)
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

func TestMigrationBackfillsOneUserChangePerAccount(t *testing.T) {
	ctx := context.Background()
	uri := testDatabaseURI(t)
	s, err := pgstore.Open(ctx, uri, pgstore.Options{MaxConns: 2, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.MigrateUpTo(ctx, 2); err != nil {
		t.Fatalf("MigrateUpTo 2: %v", err)
	}
	raw := connect(t, uri)
	exec(t, raw, "INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by) VALUES ('shop', 'shop', 1, 1, now(), now(), 'test')")
	for id, username := range map[string]string{lanID: "lan", minhID: "minh", anID: "an"} {
		exec(t, raw, "INSERT INTO accounts (tenant_id, id, username, state, password_hash, password_must_change, password_changed_at, ver, created_at, updated_at, updated_by) VALUES ('shop', $1, $2, 1, 'hash', false, now(), 1, now(), now(), 'test')", id, username)
	}
	pending, err := s.Migrate(ctx, true)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !slices.Equal(pending, []string{"0003_directory.sql"}) {
		t.Fatalf("pending = %v; want [0003_directory.sql]", pending)
	}
	rows, err := raw.Query(ctx, "SELECT a.username, a.display_name, a.avatar_url, d.kind, d.ver FROM accounts a JOIN directory_changes d ON d.tenant_id = a.tenant_id AND d.id = a.id ORDER BY a.username")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	type row struct {
		Username, DisplayName, AvatarURL string
		Kind                             int16
		Ver                              int64
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := []row{{"an", "an", "", userKind, 1}, {"lan", "lan", "", userKind, 1}, {"minh", "minh", "", userKind, 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("backfill = %+v; want %+v", got, want)
	}
	var total, xids int
	if err := raw.QueryRow(ctx, "SELECT count(*), count(DISTINCT change_xid) FROM directory_changes").Scan(&total, &xids); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 || xids != 1 {
		t.Fatalf("directory_changes rows = %d, distinct change_xid = %d; want 3, 1", total, xids)
	}
}

func TestProfileUpdateBumpsVerOnlyWhenAFieldChanges(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", lanID, "lan")
	wantVer(t, s, userKind, lanID, 1, "create")
	var displayName string
	if err := s.raw.QueryRow(ctx, "SELECT display_name FROM accounts WHERE id = $1", lanID).Scan(&displayName); err != nil || displayName != "lan" {
		t.Fatalf("display_name = %q, %v; want lan", displayName, err)
	}
	exec(t, s.raw, "UPDATE accounts SET display_name = 'Lan Nguyen' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 2, "display name changed")
	exec(t, s.raw, "UPDATE accounts SET display_name = 'Lan Nguyen' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 2, "same display name")
	change := store.PasswordChange{TenantID: "shop", AccountID: lanID, ExpectVer: a.Ver, Hash: a.PasswordHash + "x", Revocation: revocation()}
	if err := s.SetPassword(ctx, change, audit("password_change")); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	wantVer(t, s, userKind, lanID, 2, "password change")
	never := func(int) time.Time { return time.Time{} }
	if err := s.RecordLoginFailure(ctx, "shop", lanID, never, audit("login_failed")); err != nil {
		t.Fatalf("RecordLoginFailure: %v", err)
	}
	wantVer(t, s, userKind, lanID, 2, "login failure")
	exec(t, s.raw, "UPDATE accounts SET avatar_url = 'https://cdn.example/lan.png' WHERE id = $1", lanID)
	wantVer(t, s, userKind, lanID, 3, "avatar changed")
	if err := s.SetAccountState(ctx, "shop", lanID, store.StateDisabled, revocation(), audit("account_disable")); err != nil {
		t.Fatalf("SetAccountState: %v", err)
	}
	wantVer(t, s, userKind, lanID, 4, "state changed")
}

func TestAccountGroupsChangeBumpsTheUser(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", lanID, "lan")
	insertGroup(t, s, "sales", "Sales", nil)
	exec(t, s.raw, "INSERT INTO account_groups (tenant_id, account_id, group_id) VALUES ('shop', $1, 'sales')", lanID)
	wantVer(t, s, userKind, lanID, 2, "joined")
	wantVer(t, s, groupKind, "sales", 1, "group after join")
	exec(t, s.raw, "DELETE FROM account_groups WHERE tenant_id = 'shop' AND account_id = $1", lanID)
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
	seedAccount(t, s, "shop", lanID, "lan")
	seedAccount(t, s, "shop", minhID, "minh")
	listener := listen(t, s.uri)
	marker := connect(t, s.uri)
	tx, err := s.raw.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	exec(t, tx, "UPDATE accounts SET display_name = 'Lan Nguyen' WHERE id = $1", lanID)
	exec(t, tx, "UPDATE accounts SET display_name = 'Minh Tran' WHERE id = $1", minhID)
	wantNotifications(t, listener, marker, "before-commit")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	wantNotifications(t, listener, marker, "after-commit", "dir")
	tx, err = s.raw.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	exec(t, tx, "UPDATE accounts SET display_name = 'Lan' WHERE id = $1", lanID)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	wantNotifications(t, listener, marker, "after-rollback")
}

func TestRevocationInsertNotifies(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	lan := seedAccount(t, s, "shop", lanID, "lan")
	seedAccount(t, s, "shop", minhID, "minh")
	seedSessions(t, s, lan, phoneID)
	listener := listen(t, s.uri)
	marker := connect(t, s.uri)
	if err := s.SetAccountState(ctx, "shop", lanID, store.StateDisabled, revocation(), audit("account_disable")); err != nil {
		t.Fatalf("SetAccountState lan: %v", err)
	}
	wantNotifications(t, listener, marker, "account-with-session", "dir", "rev")
	if err := s.SetAccountState(ctx, "shop", minhID, store.StateDisabled, revocation(), audit("account_disable")); err != nil {
		t.Fatalf("SetAccountState minh: %v", err)
	}
	wantNotifications(t, listener, marker, "account-without-session", "dir")
	if err := s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable")); err != nil {
		t.Fatalf("SetTenantState: %v", err)
	}
	wantNotifications(t, listener, marker, "tenant", "rev")
	exec(t, s.raw, "UPDATE tenants SET name = 'Shop' WHERE id = 'shop'")
	wantNotifications(t, listener, marker, "tenant-rename")
}
