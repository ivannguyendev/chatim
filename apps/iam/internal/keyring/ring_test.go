package keyring_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var t0 = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

const sid = "aaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeSource struct {
	mu   sync.Mutex
	keys []store.SigningKey
	err  error
}

func (f *fakeSource) SigningKeys(context.Context) ([]store.SigningKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.SigningKey(nil), f.keys...), f.err
}

type fixture struct {
	src    *fakeSource
	sealer *signing.Sealer
	ring   *keyring.Ring
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	kek := make([]byte, signing.KEKLength)
	_, _ = rand.Read(kek)
	sealer, err := signing.NewSealer(kek, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{src: &fakeSource{}, sealer: sealer, now: t0}
	f.ring = keyring.New(f.src, sealer, keyring.Config{
		Token:  signing.TokenConfig{Issuer: "iss", Audience: "aud", TTL: 15 * time.Minute},
		Leeway: 30 * time.Second,
		Now:    func() time.Time { return f.now },
	})
	return f
}

func (f *fixture) addKey(t *testing.T, state store.KeyState, retired time.Time) signing.Key {
	t.Helper()
	k, _ := signing.Generate(rand.Reader)
	sealed, err := f.sealer.Seal(k)
	if err != nil {
		t.Fatal(err)
	}
	pub := k.PublicKey()
	f.src.keys = append(f.src.keys, store.SigningKey{ID: k.ID, State: state, PublicKey: pub.Key, PrivateSealed: sealed, CreatedAt: t0, RetiredAt: retired})
	return k
}

func (f *fixture) sign(t *testing.T, sid string) string {
	t.Helper()
	signer, err := f.ring.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	token, _, err := signer.Sign(signing.Claims{Tenant: "shop", Subject: "lan", Session: sid, IssuedAt: f.now})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestActiveKeySignsAndVerifiesItsOwnTokens(t *testing.T) {
	f := newFixture(t)
	f.addKey(t, store.KeyActive, time.Time{})
	if err := f.ring.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !f.ring.Ready() {
		t.Fatal("ring not ready with an active key")
	}
	c, err := f.ring.Caller(f.sign(t, sid))
	if err != nil {
		t.Fatalf("Caller: %v", err)
	}
	if c.Tenant != "shop" || c.Account != "lan" || c.Session != sid {
		t.Fatalf("caller = %+v", c)
	}
}

func TestNoActiveKeyMeansNotReady(t *testing.T) {
	f := newFixture(t)
	f.addKey(t, store.KeyPending, time.Time{})
	if err := f.ring.Reload(context.Background()); !errors.Is(err, keyring.ErrNoActiveKey) {
		t.Fatalf("Reload = %v, want ErrNoActiveKey", err)
	}
	if f.ring.Ready() {
		t.Fatal("ring ready without an active key")
	}
	if _, err := f.ring.Current(); err == nil {
		t.Fatal("Current returned a signer without an active key")
	}
}

func TestRotationKeepsOldTokensValidUntilTheRetiredKeyAges(t *testing.T) {
	f := newFixture(t)
	f.addKey(t, store.KeyActive, time.Time{})
	_ = f.ring.Reload(context.Background())
	old := f.sign(t, sid)
	f.src.keys[0].State, f.src.keys[0].RetiredAt = store.KeyRetired, f.now
	f.addKey(t, store.KeyActive, time.Time{})
	f.now = t0.Add(10 * time.Minute)
	if err := f.ring.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, err := f.ring.Caller(old); err != nil {
		t.Fatalf("token of a recently retired key = %v, want valid", err)
	}
	if fresh, _ := f.ring.Current(); fresh.KeyID() == f.src.keys[0].ID {
		t.Fatal("ring still signs with the retired key")
	}
	f.now = t0.Add(time.Hour)
	_ = f.ring.Reload(context.Background())
	keys := keyring.Published(f.src.keys, f.now, 15*time.Minute+30*time.Second)
	if len(keys) != 1 {
		t.Fatalf("published keys = %d, want only the active one after the retired key aged out", len(keys))
	}
}

func TestCallerRejectsTokensWithoutAValidSession(t *testing.T) {
	f := newFixture(t)
	k := f.addKey(t, store.KeyActive, time.Time{})
	_ = f.ring.Reload(context.Background())
	if _, err := f.ring.Caller(f.sign(t, "not-a-session")); !errors.Is(err, apperr.ErrUnauthenticated) {
		t.Fatalf("bad sid = %v, want unauthenticated", err)
	}
	noSid := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": "iss", "aud": "aud", "sub": "lan", "tid": "shop", "iat": f.now.Unix(), "exp": f.now.Add(time.Minute).Unix()})
	noSid.Header["kid"] = k.ID
	signed, _ := noSid.SignedString(k.Private)
	if _, err := f.ring.Caller(signed); !errors.Is(err, apperr.ErrUnauthenticated) {
		t.Fatalf("missing sid = %v, want unauthenticated", err)
	}
	if _, err := f.ring.Caller("garbage"); !errors.Is(err, apperr.ErrUnauthenticated) {
		t.Fatalf("garbage = %v, want unauthenticated", err)
	}
}

func TestReloadFailureKeepsTheLastGoodKeys(t *testing.T) {
	f := newFixture(t)
	f.addKey(t, store.KeyActive, time.Time{})
	_ = f.ring.Reload(context.Background())
	f.src.err = apperr.ErrUnavailable
	if err := f.ring.Reload(context.Background()); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Reload = %v, want ErrUnavailable", err)
	}
	if !f.ring.Ready() {
		t.Fatal("a failed reload dropped the last good keys")
	}
	if _, err := f.ring.Caller(f.sign(t, sid)); err != nil {
		t.Fatalf("Caller after a failed reload: %v", err)
	}
}

func TestJWKSServesThePublishedKeysOnly(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.ring.JWKS(); ok {
		t.Fatal("JWKS before the first reload")
	}
	active := f.addKey(t, store.KeyActive, time.Time{})
	pending := f.addKey(t, store.KeyPending, time.Time{})
	old := f.addKey(t, store.KeyRetired, t0.Add(-time.Hour))
	if err := f.ring.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	body, ok := f.ring.JWKS()
	if !ok {
		t.Fatal("JWKS missing after reload")
	}
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kids := map[string]bool{}
	for _, k := range set.Keys {
		if _, private := k["d"]; private {
			t.Fatal("JWKS holds private key material")
		}
		kids[k["kid"].(string)] = true
	}
	if !kids[active.ID] || !kids[pending.ID] || kids[old.ID] || len(kids) != 2 {
		t.Fatalf("kids = %v", kids)
	}
}
