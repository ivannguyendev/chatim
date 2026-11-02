package pgstore_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

var t0 = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

const testKeep = 16 * time.Minute

func testDatabaseURI(t testing.TB) string {
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

type fixture struct {
	*pgstore.Store
	raw *pgx.Conn
	uri string
}

func openStore(t testing.TB) fixture {
	t.Helper()
	ctx := context.Background()
	uri := testDatabaseURI(t)
	s, err := pgstore.Open(ctx, uri, pgstore.Options{MaxConns: 8, RevocationKeep: testKeep})
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
	return fixture{Store: s, raw: raw, uri: uri}
}

func audit(action string) store.AuditEvent {
	return store.AuditEvent{TenantID: "shop", At: t0, Actor: "test", Action: action, Detail: map[string]string{"k": "v"}}
}

func seedTenant(t testing.TB, s fixture, id string) {
	t.Helper()
	tenant := store.Tenant{ID: id, Name: id, State: store.StateActive, Ver: 1, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "test"}
	if err := s.CreateTenant(context.Background(), tenant, audit("tenant_create")); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
}

const seedAccountSQL = "INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash, password_must_change, password_changed_at, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $3, $3, $4, '', false, $5, 1, $5, $5, 'test')"

func seedAccount(t testing.TB, s fixture, tenant, id, username string) store.Account {
	t.Helper()
	if _, err := s.raw.Exec(context.Background(), seedAccountSQL, tenant, id, username, store.StateActive, t0); err != nil {
		t.Fatalf("seed account %s: %v", id, err)
	}
	return store.Account{TenantID: tenant, ID: id, DisplayName: username, State: store.StateActive, Ver: 1, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "test"}
}

func revocation() store.Revocation {
	return store.Revocation{By: "admin-cli", Reason: "test", At: t0.Add(time.Minute)}
}

func openWithoutKeep(uri string) (*pgstore.Store, error) {
	return pgstore.Open(context.Background(), uri, pgstore.Options{MaxConns: 1})
}
