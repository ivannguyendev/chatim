package trust_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	audience     = "chatim"
	acmeIssuer   = "https://acme.example"
	betaIssuer   = "https://beta.example"
	offIssuer    = "https://off.example"
	frozenIssuer = "https://frozen.example"
)

var testNow = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

type keys struct {
	ed     ed25519.PrivateKey
	ec     *ecdsa.PrivateKey
	rsa    *rsa.PrivateKey
	beta   ed25519.PrivateKey
	off    ed25519.PrivateKey
	frozen ed25519.PrivateKey
}

var testKeys = sync.OnceValue(func() keys {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return keys{ed: newEd(), ec: ec, rsa: rs, beta: newEd(), off: newEd(), frozen: newEd()}
})

func newEd() ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return priv
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func edJWK(priv ed25519.PrivateKey) []byte {
	pub := priv.Public().(ed25519.PublicKey)
	return mustJSON(map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64(pub)})
}

func ecJWK(priv *ecdsa.PrivateKey) []byte {
	raw, err := priv.PublicKey.Bytes()
	if err != nil {
		panic(err)
	}
	return mustJSON(map[string]any{"kty": "EC", "crv": "P-256", "x": b64(raw[1:33]), "y": b64(raw[33:])})
}

func rsaJWK(pub *rsa.PublicKey) []byte {
	return mustJSON(map[string]any{"kty": "RSA", "n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes())})
}

type fakeSource struct {
	mu      sync.Mutex
	issuers []store.TrustedIssuer
	keys    []store.TrustedKey
	err     error
	calls   int
}

func (f *fakeSource) TrustedIssuers(context.Context) ([]store.TrustedIssuer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]store.TrustedIssuer(nil), f.issuers...), nil
}

func (f *fakeSource) TrustedKeys(context.Context) ([]store.TrustedKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return append([]store.TrustedKey(nil), f.keys...), nil
}

func (f *fakeSource) update(change func(*fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeSource) loadCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSource) removeKey(issuer, kid string) {
	f.update(func(f *fakeSource) {
		kept := f.keys[:0]
		for _, k := range f.keys {
			if k.Issuer != issuer || k.KeyID != kid {
				kept = append(kept, k)
			}
		}
		f.keys = kept
	})
}

func (f *fakeSource) setTenantState(tenant string, state store.State) {
	f.update(func(f *fakeSource) {
		for i := range f.issuers {
			if f.issuers[i].TenantID == tenant {
				f.issuers[i].TenantState = state
			}
		}
	})
}

var errSource = errors.New("postgres down")

func activeKey(issuer, kid, alg string, jwk []byte) store.TrustedKey {
	return store.TrustedKey{Issuer: issuer, KeyID: kid, Algorithm: alg, PublicKey: jwk, State: store.StateActive}
}

func newSource() *fakeSource {
	k := testKeys()
	return &fakeSource{
		issuers: []store.TrustedIssuer{
			{Issuer: acmeIssuer, TenantID: "acme", IssuerState: store.StateActive, TenantState: store.StateActive},
			{Issuer: betaIssuer, TenantID: "beta", IssuerState: store.StateActive, TenantState: store.StateActive},
			{Issuer: offIssuer, TenantID: "acme", IssuerState: store.StateDisabled, TenantState: store.StateActive},
			{Issuer: frozenIssuer, TenantID: "frozen", IssuerState: store.StateActive, TenantState: store.StateDisabled},
		},
		keys: []store.TrustedKey{
			activeKey(acmeIssuer, "ed1", "EdDSA", edJWK(k.ed)),
			activeKey(acmeIssuer, "ec1", "ES256", ecJWK(k.ec)),
			activeKey(acmeIssuer, "rs1", "RS256", rsaJWK(&k.rsa.PublicKey)),
			activeKey(betaIssuer, "b1", "EdDSA", edJWK(k.beta)),
			activeKey(offIssuer, "off1", "EdDSA", edJWK(k.off)),
			activeKey(frozenIssuer, "fz1", "EdDSA", edJWK(k.frozen)),
			activeKey("https://ghost.example", "g1", "EdDSA", edJWK(k.off)),
		},
	}
}

func newVerifier(t *testing.T, now func() time.Time) *trust.Verifier {
	t.Helper()
	v, err := trust.New(trust.Config{Audience: audience, MaxLifetime: 10 * time.Minute, Leeway: 30 * time.Second, Now: now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func loaded(t *testing.T, src *fakeSource) *trust.Verifier {
	t.Helper()
	v := newVerifier(t, func() time.Time { return testNow })
	if _, err := v.Load(context.Background(), src); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return v
}

func claimsAt(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": acmeIssuer,
		"sub": "ha",
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
}

func baseClaims() jwt.MapClaims { return claimsAt(testNow) }

func signWith(t *testing.T, method jwt.SigningMethod, key any, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return s
}

func signEd(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	return signWith(t, jwt.SigningMethodEdDSA, testKeys().ed, "ed1", claims)
}

func withClaims(change func(jwt.MapClaims)) jwt.MapClaims {
	c := baseClaims()
	change(c)
	return c
}

func swapPayload(token string, claims jwt.MapClaims) string {
	parts := strings.Split(token, ".")
	parts[1] = b64(mustJSON(claims))
	return strings.Join(parts, ".")
}

func withHeader(t *testing.T, header map[string]any, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header = header
	s, err := tok.SignedString(testKeys().ed)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return s
}
