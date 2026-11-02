package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	shopIssuer = "https://login.shop.example"
	blogIssuer = "https://login.blog.example"
)

func trustedKey(issuer, kid string) store.TrustedKey {
	return store.TrustedKey{
		Issuer: issuer, KeyID: kid, Algorithm: "EdDSA",
		PublicKey: []byte(`{"kty":"OKP","crv":"Ed25519","x":"` + kid + `"}`),
	}
}

func seedIssuer(t *testing.T, s fixture, issuer, tenant string) {
	t.Helper()
	if err := s.CreateTrustedIssuer(context.Background(), issuer, tenant, audit("issuer_add")); err != nil {
		t.Fatalf("CreateTrustedIssuer %s: %v", issuer, err)
	}
}

func addKeys(t *testing.T, s fixture, keys ...store.TrustedKey) {
	t.Helper()
	for _, k := range keys {
		if err := s.AddTrustedKey(context.Background(), k, audit("issuer_key_add")); err != nil {
			t.Fatalf("AddTrustedKey %s: %v", k.KeyID, err)
		}
	}
}

func auditCount(t *testing.T, s fixture, action string) int {
	t.Helper()
	var n int
	if err := s.raw.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE action = $1", action).Scan(&n); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return n
}

func keyIDs(keys []store.TrustedKey) []string {
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.KeyID)
	}
	return ids
}

func sameJSON(t *testing.T, got, want []byte) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("decode stored key %q: %v", got, err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatalf("decode wanted key %q: %v", want, err)
	}
	return reflect.DeepEqual(a, b)
}

func TestOneIssuerBelongsToOneTenant(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedTenant(t, s, "blog")
	seedIssuer(t, s, shopIssuer, "shop")
	wantErr(t, s.CreateTrustedIssuer(ctx, shopIssuer, "blog", audit("issuer_add")), apperr.ErrAlreadyExists, "same issuer for a second tenant")
	wantErr(t, s.CreateTrustedIssuer(ctx, blogIssuer, "ghost", audit("issuer_add")), apperr.ErrNotFound, "issuer for an unknown tenant")
	issuers, err := s.TrustedIssuers(ctx)
	if err != nil {
		t.Fatalf("TrustedIssuers: %v", err)
	}
	want := []store.TrustedIssuer{{Issuer: shopIssuer, TenantID: "shop", IssuerState: store.StateActive, TenantState: store.StateActive}}
	if !reflect.DeepEqual(issuers, want) {
		t.Fatalf("TrustedIssuers = %+v; want %+v", issuers, want)
	}
	if n := auditCount(t, s, "issuer_add"); n != 1 {
		t.Fatalf("issuer_add audit rows = %d; want 1", n)
	}
}

func TestTrustedIssuersCarryIssuerAndTenantState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedTenant(t, s, "blog")
	seedIssuer(t, s, shopIssuer, "shop")
	seedIssuer(t, s, blogIssuer, "blog")
	if err := s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable")); err != nil {
		t.Fatalf("SetTenantState: %v", err)
	}
	if err := s.SetTrustedIssuerState(ctx, blogIssuer, store.StateDisabled, audit("issuer_disable")); err != nil {
		t.Fatalf("SetTrustedIssuerState: %v", err)
	}
	wantErr(t, s.SetTrustedIssuerState(ctx, "https://ghost.example", store.StateDisabled, audit("issuer_disable")), apperr.ErrNotFound, "unknown issuer")
	issuers, err := s.TrustedIssuers(ctx)
	if err != nil {
		t.Fatalf("TrustedIssuers: %v", err)
	}
	want := []store.TrustedIssuer{
		{Issuer: blogIssuer, TenantID: "blog", IssuerState: store.StateDisabled, TenantState: store.StateActive},
		{Issuer: shopIssuer, TenantID: "shop", IssuerState: store.StateActive, TenantState: store.StateDisabled},
	}
	if !reflect.DeepEqual(issuers, want) {
		t.Fatalf("TrustedIssuers = %+v; want %+v", issuers, want)
	}
	var ver int64
	if err := s.raw.QueryRow(ctx, "SELECT ver FROM trusted_issuers WHERE issuer = $1", blogIssuer).Scan(&ver); err != nil || ver != 2 {
		t.Fatalf("issuer ver = %d, %v; want 2", ver, err)
	}
	if n := auditCount(t, s, "issuer_disable"); n != 1 {
		t.Fatalf("issuer_disable audit rows = %d; want 1", n)
	}
}

func TestAddTrustedKeyNeedsAKnownIssuerAndAFreeKeyID(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedIssuer(t, s, shopIssuer, "shop")
	wantErr(t, s.AddTrustedKey(ctx, trustedKey("https://ghost.example", "k1"), audit("issuer_key_add")), apperr.ErrNotFound, "key for an unknown issuer")
	addKeys(t, s, trustedKey(shopIssuer, "k1"))
	wantErr(t, s.AddTrustedKey(ctx, trustedKey(shopIssuer, "k1"), audit("issuer_key_add")), apperr.ErrAlreadyExists, "same kid twice")
	if n := auditCount(t, s, "issuer_key_add"); n != 1 {
		t.Fatalf("issuer_key_add audit rows = %d; want 1", n)
	}
}

func TestRemovedTrustedKeysLeaveTheActiveSet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedIssuer(t, s, shopIssuer, "shop")
	addKeys(t, s, trustedKey(shopIssuer, "k1"), trustedKey(shopIssuer, "k2"))
	if err := s.RemoveTrustedKey(ctx, shopIssuer, "k1", audit("issuer_key_remove")); err != nil {
		t.Fatalf("RemoveTrustedKey: %v", err)
	}
	wantErr(t, s.RemoveTrustedKey(ctx, shopIssuer, "k1", audit("issuer_key_remove")), apperr.ErrNotFound, "remove twice")
	wantErr(t, s.RemoveTrustedKey(ctx, shopIssuer, "k9", audit("issuer_key_remove")), apperr.ErrNotFound, "remove unknown kid")
	active, err := s.TrustedKeys(ctx)
	if err != nil {
		t.Fatalf("TrustedKeys: %v", err)
	}
	if len(active) != 1 || active[0].KeyID != "k2" || active[0].Issuer != shopIssuer || active[0].Algorithm != "EdDSA" || active[0].State != store.StateActive {
		t.Fatalf("TrustedKeys = %+v; want only k2 active", active)
	}
	if !sameJSON(t, active[0].PublicKey, trustedKey(shopIssuer, "k2").PublicKey) {
		t.Fatalf("stored public key = %s", active[0].PublicKey)
	}
	all, err := s.TrustedKeysOf(ctx, shopIssuer)
	if err != nil {
		t.Fatalf("TrustedKeysOf: %v", err)
	}
	if !reflect.DeepEqual(keyIDs(all), []string{"k1", "k2"}) || all[0].State != store.StateDisabled || all[1].State != store.StateActive {
		t.Fatalf("TrustedKeysOf = %+v; want k1 removed, k2 active", all)
	}
	if !all[0].CreatedAt.Equal(t0) || all[0].CreatedBy != "test" {
		t.Fatalf("k1 created = %v by %q; want %v by test", all[0].CreatedAt, all[0].CreatedBy, t0)
	}
	if n := auditCount(t, s, "issuer_key_remove"); n != 1 {
		t.Fatalf("issuer_key_remove audit rows = %d; want 1", n)
	}
}

func TestTrustReadsFailUnavailableWhenPostgresIsGone(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	closed, err := pgstore.Open(ctx, s.uri, pgstore.Options{MaxConns: 1, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	closed.Close()
	if _, err := closed.TrustedIssuers(ctx); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("TrustedIssuers on a closed pool = %v; want ErrUnavailable", err)
	}
	if _, err := closed.TrustedKeys(ctx); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("TrustedKeys on a closed pool = %v; want ErrUnavailable", err)
	}
}
