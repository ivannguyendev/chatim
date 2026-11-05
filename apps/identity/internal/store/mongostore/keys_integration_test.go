package mongostore

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type claimDoc struct {
	KeyID string `bson:"key_id"`
	Ver   int64  `bson:"ver"`
}

func newSigningKey(id string) store.SigningKey {
	return store.SigningKey{ID: id, PublicKey: []byte(`{"kid":"` + id + `"}`), PrivateSealed: []byte{0, 1, 2, 0xff}}
}

func insertPendingKey(t *testing.T, s *Store, id string, createdAt time.Time) {
	t.Helper()
	k := newSigningKey(id)
	insertDoc(t, s, signingKeysCollection, bson.D{
		{Key: "_id", Value: id},
		{Key: "state", Value: store.KeyPending},
		{Key: "public_key", Value: k.PublicKey},
		{Key: "private_key_sealed", Value: k.PrivateSealed},
		{Key: "created_at", Value: createdAt},
	})
}

func signingKeysByID(t *testing.T, s *Store) map[string]store.SigningKey {
	t.Helper()
	keys, err := s.SigningKeys(t.Context())
	if err != nil {
		t.Fatalf("SigningKeys: %v", err)
	}
	out := make(map[string]store.SigningKey, len(keys))
	for _, k := range keys {
		out[k.ID] = k
	}
	return out
}

func activeKeyIDs(t *testing.T, s *Store) []string {
	t.Helper()
	var ids []string
	for id, k := range signingKeysByID(t, s) {
		if k.State == store.KeyActive {
			ids = append(ids, id)
		}
	}
	return ids
}

func readClaim(t *testing.T, s *Store) (claimDoc, bool) {
	t.Helper()
	var doc claimDoc
	err := s.db.Collection(signingKeyClaimsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: "active"}}).Decode(&doc)
	if err != nil {
		return doc, false
	}
	return doc, true
}

func mustActivate(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.ActivateSigningKey(t.Context(), id, 0); err != nil {
		t.Fatalf("ActivateSigningKey %s: %v", id, err)
	}
}

func TestCreateSigningKeyStampsServerTimeAndRefusesADuplicate(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	before := serverNow(t, s)
	created, err := s.CreateSigningKey(t.Context(), newSigningKey("k1"))
	if err != nil {
		t.Fatalf("CreateSigningKey: %v", err)
	}
	after := serverNow(t, s)
	if created.Before(before) || created.After(after) {
		t.Fatalf("created_at = %v; want server time within [%v, %v]", created, before, after)
	}
	got := signingKeysByID(t, s)["k1"]
	want := newSigningKey("k1")
	if got.State != store.KeyPending || !bytes.Equal(got.PublicKey, want.PublicKey) || !bytes.Equal(got.PrivateSealed, want.PrivateSealed) ||
		!got.CreatedAt.Equal(created) || !got.ActivatedAt.IsZero() || !got.RetiredAt.IsZero() {
		t.Fatalf("key = %+v; want pending %+v created at %v", got, want, created)
	}
	other := store.SigningKey{ID: "k1", PublicKey: []byte("other"), PrivateSealed: []byte("other")}
	if _, err := s.CreateSigningKey(t.Context(), other); !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("duplicate kid = %v; want already exists", err)
	}
	if again := signingKeysByID(t, s)["k1"]; !bytes.Equal(again.PublicKey, want.PublicKey) || !again.CreatedAt.Equal(created) {
		t.Fatalf("key after the duplicate = %+v; want it unchanged", again)
	}
}

func TestActivateSigningKeyWithNoLeadPointsTheClaimAndIsIdempotent(t *testing.T) {
	s := testDatabase(t)
	if _, err := s.CreateSigningKey(t.Context(), newSigningKey("k1")); err != nil {
		t.Fatalf("CreateSigningKey: %v", err)
	}
	mustActivate(t, s, "k1")
	got := signingKeysByID(t, s)["k1"]
	if got.State != store.KeyActive || got.ActivatedAt.IsZero() || !got.RetiredAt.IsZero() {
		t.Fatalf("key = %+v; want active with activated_at and no retired_at", got)
	}
	if claim, ok := readClaim(t, s); !ok || claim != (claimDoc{KeyID: "k1", Ver: 1}) {
		t.Fatalf("claim = %+v, %v; want k1 ver 1", claim, ok)
	}
	mustActivate(t, s, "k1")
	if claim, _ := readClaim(t, s); claim != (claimDoc{KeyID: "k1", Ver: 1}) {
		t.Fatalf("claim after activating the active key = %+v; want k1 ver 1 unchanged", claim)
	}
	if again := signingKeysByID(t, s)["k1"]; !again.ActivatedAt.Equal(got.ActivatedAt) {
		t.Fatalf("activated_at = %v; want %v unchanged", again.ActivatedAt, got.ActivatedAt)
	}
	if err := s.ActivateSigningKey(t.Context(), "ghost", 0); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("activate an unknown key = %v; want not found", err)
	}
}

func TestActivatingASecondKeyRetiresTheFirstAndARetiredKeyIsRefused(t *testing.T) {
	s := testDatabase(t)
	for _, id := range []string{"k1", "k2"} {
		if _, err := s.CreateSigningKey(t.Context(), newSigningKey(id)); err != nil {
			t.Fatalf("CreateSigningKey %s: %v", id, err)
		}
	}
	mustActivate(t, s, "k1")
	mustActivate(t, s, "k2")
	keys := signingKeysByID(t, s)
	if k1 := keys["k1"]; k1.State != store.KeyRetired || k1.RetiredAt.IsZero() {
		t.Fatalf("k1 = %+v; want retired with retired_at", k1)
	}
	if active := activeKeyIDs(t, s); !slices.Equal(active, []string{"k2"}) {
		t.Fatalf("active keys = %v; want only k2", active)
	}
	if claim, _ := readClaim(t, s); claim != (claimDoc{KeyID: "k2", Ver: 2}) {
		t.Fatalf("claim = %+v; want k2 ver 2", claim)
	}
	err := s.ActivateSigningKey(t.Context(), "k1", 0)
	if !errors.Is(err, apperr.ErrFailedPrecondition) || !strings.Contains(err.Error(), "signing key k1 is retired") {
		t.Fatalf("activate a retired key = %v; want failed precondition, signing key k1 is retired", err)
	}
}

func TestActivateTooEarlyNamesTheTimeAndWritesNothing(t *testing.T) {
	s := testDatabase(t)
	created, err := s.CreateSigningKey(t.Context(), newSigningKey("k1"))
	if err != nil {
		t.Fatalf("CreateSigningKey: %v", err)
	}
	err = s.ActivateSigningKey(t.Context(), "k1", time.Hour)
	want := "signing key k1 can be activated from " + created.Add(time.Hour).UTC().Format(time.RFC3339) + ", once every node has fetched it"
	if !errors.Is(err, apperr.ErrFailedPrecondition) || !strings.Contains(err.Error(), want) {
		t.Fatalf("activate too early = %v; want failed precondition with %q", err, want)
	}
	if k1 := signingKeysByID(t, s)["k1"]; k1.State != store.KeyPending || !k1.ActivatedAt.IsZero() {
		t.Fatalf("k1 = %+v; want still pending", k1)
	}
	if claim, ok := readClaim(t, s); ok {
		t.Fatalf("claim = %+v; want none after the abort", claim)
	}
}

func TestActivatingANewerKeyRetiresOlderPendingKeysOnly(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	now := serverNow(t, s)
	insertPendingKey(t, s, "old", now.Add(-3*time.Minute))
	insertPendingKey(t, s, "mid", now.Add(-2*time.Minute))
	insertPendingKey(t, s, "new", now.Add(-time.Minute))
	mustActivate(t, s, "mid")
	keys := signingKeysByID(t, s)
	if keys["old"].State != store.KeyRetired || keys["mid"].State != store.KeyActive || keys["new"].State != store.KeyPending {
		t.Fatalf("states old %d mid %d new %d; want retired, active, pending", keys["old"].State, keys["mid"].State, keys["new"].State)
	}
	got, err := s.SigningKeys(t.Context())
	if err != nil {
		t.Fatalf("SigningKeys: %v", err)
	}
	order := make([]string, 0, len(got))
	for _, k := range got {
		order = append(order, k.ID)
	}
	if !slices.Equal(order, []string{"old", "mid", "new"}) {
		t.Fatalf("SigningKeys order = %v; want by created_at", order)
	}
}
