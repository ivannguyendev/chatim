package pgstore_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store/pgstore"
)

var schemaTables = []string{
	"audit_events", "directory_changes", "groups", "revoked_sessions", "schema_migrations",
	"signing_keys", "tenants", "trusted_issuers", "trusted_keys", "user_groups", "users",
}

var userColumns = []string{
	"tenant_id", "id", "display_name", "avatar_url", "state", "ver", "created_at", "updated_at", "updated_by",
}

func columnsOf(t *testing.T, s fixture, table string) []string {
	t.Helper()
	return queryStrings(t, s.raw,
		"SELECT column_name::text FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 ORDER BY ordinal_position", table)
}

func TestSquashedMigrationBuildsTheIdentitySchema(t *testing.T) {
	s := openStore(t)
	if v := pgstore.LatestVersion(); v != 1 {
		t.Fatalf("LatestVersion = %d; want 1", v)
	}
	if got := queryStrings(t, s.raw, "SELECT version::text FROM schema_migrations ORDER BY version"); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("schema_migrations = %v; want [1]", got)
	}
	tables := queryStrings(t, s.raw, "SELECT table_name::text FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name")
	if !slices.Equal(tables, schemaTables) {
		t.Fatalf("tables = %v; want %v", tables, schemaTables)
	}
	if got := columnsOf(t, s, "users"); !slices.Equal(got, userColumns) {
		t.Fatalf("users columns = %v; want %v", got, userColumns)
	}
	if got := columnsOf(t, s, "tenants"); !slices.Contains(got, "sessions_revoked_at") {
		t.Fatalf("tenants columns = %v; want sessions_revoked_at", got)
	}
	if got := columnsOf(t, s, "audit_events"); !slices.Contains(got, "user_id") || slices.Contains(got, "session_id") || slices.Contains(got, "ip") {
		t.Fatalf("audit_events columns = %v; want user_id without session_id and ip", got)
	}
	pending, err := s.Migrate(context.Background(), true)
	if err != nil || len(pending) != 0 {
		t.Fatalf("second Migrate = %v, %v; want nothing to apply", pending, err)
	}
}

func TestTrustedKeysRefuseUnknownAlgorithmsAndIssuers(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	exec(t, s.raw, "INSERT INTO trusted_issuers (issuer, tenant_id, state, ver, created_at, updated_at, updated_by) VALUES ('shop-backend', 'shop', 1, 1, now(), now(), 'test')")
	const insertKey = "INSERT INTO trusted_keys (issuer, key_id, algorithm, public_key, state, created_at, created_by) VALUES ($1, $2, $3, '{}'::jsonb, 1, now(), 'test')"
	for _, alg := range []string{"EdDSA", "ES256", "RS256"} {
		exec(t, s.raw, insertKey, "shop-backend", "k-"+alg, alg)
	}
	_, err := s.raw.Exec(ctx, insertKey, "shop-backend", "k-hs", "HS256")
	wantPgError(t, err, "23514", "trusted_keys_algorithm_check", "HS256 key")
	_, err = s.raw.Exec(ctx, insertKey, "nobody", "k1", "ES256")
	wantPgError(t, err, "23503", "trusted_keys_issuer_fkey", "key of an unknown issuer")
	_, err = s.raw.Exec(ctx, insertKey, "shop-backend", "k-ES256", "ES256")
	wantPgError(t, err, "23505", "trusted_keys_pkey", "same key id twice")
	_, err = s.raw.Exec(ctx, "INSERT INTO trusted_issuers (issuer, tenant_id, state, ver, created_at, updated_at, updated_by) VALUES ('ghost-backend', 'ghost', 1, 1, now(), now(), 'test')")
	wantPgError(t, err, "23503", "trusted_issuers_tenant_id_fkey", "issuer of an unknown tenant")
}
