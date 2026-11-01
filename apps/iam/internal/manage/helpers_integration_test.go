package manage_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

var storyStart = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

const (
	storyIdle = 30 * 24 * time.Hour
	storyHard = 90 * 24 * time.Hour
)

type pgWorld struct {
	store *pgstore.Store
	raw   *pgx.Conn
	svc   *manage.Service
}

func storyDatabaseURI(t testing.TB) string {
	t.Helper()
	base := os.Getenv("CHATIM_IT_PG_URI")
	if base == "" {
		t.Skip("set CHATIM_IT_PG_URI to run")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	name := "iam_it_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func openWorld(t testing.TB, maxConns int32, adminSlots int) pgWorld {
	t.Helper()
	ctx := context.Background()
	uri := storyDatabaseURI(t)
	s, err := pgstore.Open(ctx, uri, pgstore.Options{MaxConns: maxConns, RevocationKeep: 16 * time.Minute, AdminDBSlots: adminSlots})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Migrate(ctx, true); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	raw, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("connect raw: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close(context.Background()) })
	return pgWorld{store: s, raw: raw, svc: serviceAt(s, storyStart.Add(time.Hour))}
}

func serviceAt(runner store.AdminRunner, at time.Time) *manage.Service {
	return manage.New(manage.Deps{Store: runner, Now: func() time.Time { return at }})
}

func seedAudit(tenantID, action string) store.AuditEvent {
	return store.AuditEvent{TenantID: tenantID, At: storyStart, Actor: "seed", Action: action, Detail: map[string]string{}}
}

func must(t testing.TB, step string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

func (w pgWorld) tenant(t testing.TB, id string) {
	t.Helper()
	tn := store.Tenant{ID: id, Name: id, State: store.StateActive, Ver: 1, CreatedAt: storyStart, UpdatedAt: storyStart, UpdatedBy: "seed"}
	must(t, "CreateTenant "+id, w.store.CreateTenant(context.Background(), tn, seedAudit(id, "tenant_create")))
}

func (w pgWorld) account(t testing.TB, tenantID, id string) auth.Caller {
	t.Helper()
	ctx := context.Background()
	a := store.Account{
		TenantID: tenantID, ID: id, Username: id, State: store.StateActive,
		PasswordHash:      "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		PasswordChangedAt: storyStart, Ver: 1, CreatedAt: storyStart, UpdatedAt: storyStart, UpdatedBy: "seed",
	}
	must(t, "CreateAccount "+id, w.store.CreateAccount(ctx, a, seedAudit(tenantID, "account_create")))
	session := store.NewSession{AccountVer: a.Ver, Session: store.Session{
		ID: "s-" + tenantID + "-" + id, TenantID: tenantID, AccountID: id, State: store.StateActive,
		DeviceName: "laptop", UserAgent: "test", CreatedAt: storyStart, LastUsedAt: storyStart,
		IdleExpiresAt: storyStart.Add(storyIdle), ExpiresAt: storyStart.Add(storyHard),
		RefreshVer: 1, RefreshHash: []byte("hash-" + id), KeyThumbprint: "jkt-" + id,
	}}
	_, err := w.store.CreateSession(ctx, session, seedAudit(tenantID, "login_ok"))
	must(t, "CreateSession "+id, err)
	return auth.Caller{Tenant: tenantID, Account: id, Session: session.Session.ID, IP: "10.0.0.1"}
}

func (w pgWorld) group(t testing.TB, tenantID, id, parent string) {
	t.Helper()
	g := store.Group{
		TenantID: tenantID, ID: id, Name: "Group " + id, ParentID: parent, Mentionable: true,
		State: store.GroupLive, CreatedAt: storyStart, UpdatedAt: storyStart, UpdatedBy: "seed",
	}
	must(t, "CreateGroup "+id, w.store.CreateGroup(context.Background(), g, seedAudit(tenantID, "group_create")))
}

func (w pgWorld) role(t testing.TB, tenantID, id string, perms ...string) {
	t.Helper()
	r := store.Role{
		TenantID: tenantID, ID: id, Name: "Role " + id, Ver: 1, Permissions: perms,
		CreatedAt: storyStart, UpdatedAt: storyStart, UpdatedBy: "seed",
	}
	must(t, "CreateRole "+id, w.store.CreateRole(context.Background(), r, seedAudit(tenantID, "role_create")))
}

func (w pgWorld) joinGroups(t testing.TB, tenantID, account string, groups ...string) {
	t.Helper()
	must(t, "SetAccountGroups "+account, w.store.SetAccountGroups(context.Background(), tenantID, account, groups, seedAudit(tenantID, "account_groups_set")))
}

func (w pgWorld) accountRoles(t testing.TB, tenantID, account string, roles ...string) {
	t.Helper()
	must(t, "SetAccountRoles "+account, w.store.SetAccountRoles(context.Background(), tenantID, account, roles, seedAudit(tenantID, "account_roles_set")))
}

func (w pgWorld) groupRoles(t testing.TB, tenantID, group string, roles ...string) {
	t.Helper()
	must(t, "SetGroupRoles "+group, w.store.SetGroupRoles(context.Background(), tenantID, group, roles, seedAudit(tenantID, "group_roles_set")))
}

func (w pgWorld) strings(t testing.TB, sql string, args ...any) []string {
	t.Helper()
	rows, err := w.raw.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

func (w pgWorld) groupsOf(t testing.TB, tenantID, account string) []string {
	t.Helper()
	return w.strings(t, "SELECT group_id FROM account_groups WHERE tenant_id = $1 AND account_id = $2 ORDER BY group_id", tenantID, account)
}

func (w pgWorld) parentOf(t testing.TB, tenantID, group string) []string {
	t.Helper()
	return w.strings(t, "SELECT coalesce(parent_id, '') FROM groups WHERE tenant_id = $1 AND id = $2", tenantID, group)
}

func (w pgWorld) audits(t testing.TB, tenantID, actor, action string) int {
	t.Helper()
	var n int
	err := w.raw.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND actor = $2 AND action = $3", tenantID, actor, action).Scan(&n)
	must(t, "count audits", err)
	return n
}

func wantCode(t testing.TB, step string, err, code error) {
	t.Helper()
	if !errors.Is(err, code) {
		t.Fatalf("%s: err = %v; want %v", step, err, code)
	}
}

func wantList(t testing.TB, step string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) && len(got)+len(want) > 0 {
		t.Fatalf("%s: got %v; want %v", step, got, want)
	}
}
