package mongostore

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type trustedIssuerDocument struct {
	ID        string    `bson:"_id"`
	TenantID  string    `bson:"tenant_id"`
	State     int32     `bson:"state"`
	Ver       int64     `bson:"ver"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
	UpdatedBy string    `bson:"updated_by"`
}

func readTrustedIssuer(t *testing.T, s *Store, issuer string) trustedIssuerDocument {
	t.Helper()
	var doc trustedIssuerDocument
	if err := s.db.Collection(trustedIssuersCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: issuer}}).Decode(&doc); err != nil {
		t.Fatalf("read issuer %s: %v", issuer, err)
	}
	return doc
}

func canonicalEdJWK(t *testing.T, kid string) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub), "kid": kid})
	if err != nil {
		t.Fatalf("marshal jwk: %v", err)
	}
	_, canonical, err := trust.ParsePublicJWK(trust.AlgorithmEdDSA, raw)
	if err != nil {
		t.Fatalf("ParsePublicJWK: %v", err)
	}
	return canonical
}

func mustCreateIssuer(t *testing.T, s *Store, issuer, tenantID string) {
	t.Helper()
	if err := s.CreateTrustedIssuer(t.Context(), issuer, tenantID, itAt, "cli:admin"); err != nil {
		t.Fatalf("CreateTrustedIssuer %s: %v", issuer, err)
	}
}

func mustAddKey(t *testing.T, s *Store, issuer, kid string, at time.Time) store.TrustedKey {
	t.Helper()
	k := store.TrustedKey{Issuer: issuer, KeyID: kid, Algorithm: trust.AlgorithmEdDSA, PublicKey: canonicalEdJWK(t, kid)}
	if err := s.AddTrustedKey(t.Context(), k, at, "cli:admin"); err != nil {
		t.Fatalf("AddTrustedKey %s/%s: %v", issuer, kid, err)
	}
	return k
}

func keyIDs(keys []store.TrustedKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Issuer+"/"+k.KeyID)
	}
	return out
}

func TestCreateTrustedIssuerNeedsTheTenantAndRefusesADuplicate(t *testing.T) {
	s := testDatabase(t)
	if err := s.CreateTrustedIssuer(t.Context(), "shop-backend", "ghost", itAt, "cli:admin"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("issuer for an unknown tenant = %v; want not found", err)
	}
	mustCreateTenant(t, s, "shop")
	mustCreateIssuer(t, s, "shop-backend", "shop")
	got := readTrustedIssuer(t, s, "shop-backend")
	if got.TenantID != "shop" || got.State != 1 || got.Ver != 1 || !got.CreatedAt.Equal(itAt) || !got.UpdatedAt.Equal(itAt) || got.UpdatedBy != "cli:admin" {
		t.Fatalf("issuer = %+v; want tenant shop, state 1, ver 1, at %v by cli:admin", got, itAt)
	}
	if err := s.CreateTrustedIssuer(t.Context(), "shop-backend", "shop", itAt, "cli:other"); !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("duplicate issuer = %v; want already exists", err)
	}
}

func TestTrustNamesWithTheKeySeparatorAreRefused(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuer(t, s, "shop-backend", "shop")
	if err := s.CreateTrustedIssuer(t.Context(), "shop│backend", "shop", itAt, "cli:admin"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("issuer with the separator = %v; want invalid argument", err)
	}
	k := store.TrustedKey{Issuer: "shop-backend", KeyID: "k│1", Algorithm: trust.AlgorithmEdDSA, PublicKey: canonicalEdJWK(t, "")}
	if err := s.AddTrustedKey(t.Context(), k, itAt, "cli:admin"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("kid with the separator = %v; want invalid argument", err)
	}
	if err := s.RemoveTrustedKey(t.Context(), "shop-backend", "k│1"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("remove a kid with the separator = %v; want invalid argument", err)
	}
}

func TestSetTrustedIssuerStateBumpsTheVersionOnlyOnAChange(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuer(t, s, "shop-backend", "shop")
	disableAt := itAt.Add(time.Minute)
	if err := s.SetTrustedIssuerState(t.Context(), "shop-backend", store.StateDisabled, disableAt, "cli:ops"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled := readTrustedIssuer(t, s, "shop-backend")
	if disabled.State != 2 || disabled.Ver != 2 || !disabled.UpdatedAt.Equal(disableAt) || disabled.UpdatedBy != "cli:ops" {
		t.Fatalf("disabled issuer = %+v; want state 2, ver 2, at %v by cli:ops", disabled, disableAt)
	}
	if err := s.SetTrustedIssuerState(t.Context(), "shop-backend", store.StateDisabled, disableAt.Add(time.Minute), "cli:other"); err != nil {
		t.Fatalf("disable again: %v", err)
	}
	if again := readTrustedIssuer(t, s, "shop-backend"); again != disabled {
		t.Fatalf("issuer after a second disable = %+v; want %+v", again, disabled)
	}
	enableAt := disableAt.Add(time.Hour)
	if err := s.SetTrustedIssuerState(t.Context(), "shop-backend", store.StateActive, enableAt, "cli:ops"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if enabled := readTrustedIssuer(t, s, "shop-backend"); enabled.State != 1 || enabled.Ver != 3 || !enabled.UpdatedAt.Equal(enableAt) {
		t.Fatalf("enabled issuer = %+v; want state 1, ver 3, at %v", enabled, enableAt)
	}
	if err := s.SetTrustedIssuerState(t.Context(), "ghost", store.StateDisabled, enableAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("unknown issuer = %v; want not found", err)
	}
}

func TestAddTrustedKeyNeedsTheIssuerKeepsThePublicKeyAndRefusesADuplicate(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	ghost := store.TrustedKey{Issuer: "ghost", KeyID: "k1", Algorithm: trust.AlgorithmEdDSA, PublicKey: canonicalEdJWK(t, "k1")}
	if err := s.AddTrustedKey(t.Context(), ghost, itAt, "cli:admin"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("key for an unknown issuer = %v; want not found", err)
	}
	mustCreateIssuer(t, s, "shop-backend", "shop")
	added := mustAddKey(t, s, "shop-backend", "k1", itAt)
	raw, err := s.db.Collection(trustedKeysCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: "shop-backend│k1"}}).Raw()
	if err != nil {
		t.Fatalf("read key doc: %v", err)
	}
	if stored, ok := raw.Lookup("public_key").StringValueOK(); !ok || stored != string(added.PublicKey) {
		t.Fatalf("public_key = %v; want the string %s", raw.Lookup("public_key"), added.PublicKey)
	}
	keys, err := s.TrustedKeysOf(t.Context(), "shop-backend")
	if err != nil || len(keys) != 1 {
		t.Fatalf("TrustedKeysOf = %v, %v; want one key", keys, err)
	}
	want := store.TrustedKey{Issuer: "shop-backend", KeyID: "k1", Algorithm: trust.AlgorithmEdDSA, PublicKey: added.PublicKey, State: store.StateActive, CreatedAt: itAt, CreatedBy: "cli:admin"}
	got := keys[0]
	if got.Issuer != want.Issuer || got.KeyID != want.KeyID || got.Algorithm != want.Algorithm || string(got.PublicKey) != string(want.PublicKey) ||
		got.State != want.State || !got.CreatedAt.Equal(want.CreatedAt) || got.CreatedBy != want.CreatedBy {
		t.Fatalf("key = %+v; want %+v", got, want)
	}
	if _, _, err := trust.ParsePublicJWK(got.Algorithm, got.PublicKey); err != nil {
		t.Fatalf("stored key no longer parses: %v", err)
	}
	dup := store.TrustedKey{Issuer: "shop-backend", KeyID: "k1", Algorithm: trust.AlgorithmEdDSA, PublicKey: canonicalEdJWK(t, "k1")}
	if err := s.AddTrustedKey(t.Context(), dup, itAt, "cli:admin"); !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("duplicate kid = %v; want already exists", err)
	}
}

func TestRemoveTrustedKeyTwiceReturnsNotFoundTheSecondTime(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuer(t, s, "shop-backend", "shop")
	mustAddKey(t, s, "shop-backend", "k1", itAt)
	mustAddKey(t, s, "shop-backend", "k2", itAt)
	if err := s.RemoveTrustedKey(t.Context(), "shop-backend", "k1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := s.RemoveTrustedKey(t.Context(), "shop-backend", "k1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("remove twice = %v; want not found", err)
	}
	if err := s.RemoveTrustedKey(t.Context(), "shop-backend", "ghost"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("remove an unknown key = %v; want not found", err)
	}
	active, err := s.TrustedKeys(t.Context())
	if err != nil || !slices.Equal(keyIDs(active), []string{"shop-backend/k2"}) {
		t.Fatalf("TrustedKeys = %v, %v; want only k2", keyIDs(active), err)
	}
	all, err := s.TrustedKeysOf(t.Context(), "shop-backend")
	if err != nil || len(all) != 2 || all[0].KeyID != "k1" || all[0].State != store.StateDisabled || all[1].State != store.StateActive {
		t.Fatalf("TrustedKeysOf = %+v, %v; want k1 removed and k2 active", all, err)
	}
}

func TestTrustedIssuersCarryTheTenantStateSortedByIssuer(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateTenant(t, s, "bank")
	mustCreateIssuer(t, s, "shop-idp", "shop")
	mustCreateIssuer(t, s, "bank-backend", "bank")
	mustCreateIssuer(t, s, "shop-backend", "shop")
	if err := s.SetTrustedIssuerState(t.Context(), "shop-idp", store.StateDisabled, itAt, "cli:ops"); err != nil {
		t.Fatalf("disable issuer: %v", err)
	}
	if err := s.SetTenantState(t.Context(), "shop", store.StateDisabled, store.Revocation{By: "cli:ops", At: itAt}); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	got, err := s.TrustedIssuers(t.Context())
	if err != nil {
		t.Fatalf("TrustedIssuers: %v", err)
	}
	want := []store.TrustedIssuer{
		{Issuer: "bank-backend", TenantID: "bank", IssuerState: store.StateActive, TenantState: store.StateActive},
		{Issuer: "shop-backend", TenantID: "shop", IssuerState: store.StateActive, TenantState: store.StateDisabled},
		{Issuer: "shop-idp", TenantID: "shop", IssuerState: store.StateDisabled, TenantState: store.StateDisabled},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("TrustedIssuers = %+v; want %+v", got, want)
	}
}

func TestTrustedKeysOfReadsOnlyThatIssuerInCreationOrder(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	for _, issuer := range []string{"a", "a-b", "ab"} {
		mustCreateIssuer(t, s, issuer, "shop")
	}
	mustAddKey(t, s, "a", "z", itAt)
	mustAddKey(t, s, "a", "y", itAt.Add(time.Minute))
	mustAddKey(t, s, "a", "x", itAt.Add(time.Minute))
	mustAddKey(t, s, "a-b", "k", itAt)
	mustAddKey(t, s, "ab", "k", itAt)
	got, err := s.TrustedKeysOf(t.Context(), "a")
	if err != nil || !slices.Equal(keyIDs(got), []string{"a/z", "a/x", "a/y"}) {
		t.Fatalf("TrustedKeysOf(a) = %v, %v; want a/z, a/x, a/y", keyIDs(got), err)
	}
	none, err := s.TrustedKeysOf(t.Context(), "ghost")
	if err != nil || len(none) != 0 {
		t.Fatalf("TrustedKeysOf(ghost) = %v, %v; want none", none, err)
	}
	all, err := s.TrustedKeys(t.Context())
	if err != nil || !slices.Equal(keyIDs(all), []string{"a-b/k", "ab/k", "a/x", "a/y", "a/z"}) {
		t.Fatalf("TrustedKeys = %v, %v; want every active key in _id order", keyIDs(all), err)
	}
}
