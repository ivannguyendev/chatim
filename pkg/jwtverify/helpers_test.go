package jwtverify_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

const (
	testIssuer   = "chatim-dev"
	testAudience = "chatim-websocket"
	testKID      = "dev-1"
)

var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type testKey struct {
	kid  string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newTestKey(t testing.TB, kid string) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return testKey{kid: kid, pub: pub, priv: priv}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func publicJWK(k testKey) map[string]any {
	return map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": k.kid, "x": b64(k.pub)}
}

func keySet(t testing.TB, keys ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func testConfig(jwks []byte) jwtverify.Config {
	return jwtverify.Config{
		JWKS:     jwks,
		Issuer:   testIssuer,
		Audience: testAudience,
		Leeway:   30 * time.Second,
		Now:      func() time.Time { return testNow },
	}
}

func newVerifier(t testing.TB, keys ...testKey) *jwtverify.Verifier {
	t.Helper()
	jwks := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		jwks = append(jwks, publicJWK(k))
	}
	v, err := jwtverify.New(testConfig(keySet(t, jwks...)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIssuer,
		"aud": testAudience,
		"sub": "Alice_1",
		"tid": "acme",
		"iat": testNow.Unix(),
		"exp": testNow.Add(time.Hour).Unix(),
	}
}

func signWith(t testing.TB, method jwt.SigningMethod, key any, header map[string]any, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	for name, value := range header {
		if value == nil {
			delete(token.Header, name)
			continue
		}
		token.Header[name] = value
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func signEdDSA(t testing.TB, k testKey, claims jwt.MapClaims) string {
	t.Helper()
	return signWith(t, jwt.SigningMethodEdDSA, k.priv, map[string]any{"kid": k.kid}, claims)
}

func withClaim(name string, value any) jwt.MapClaims {
	claims := validClaims()
	if value == nil {
		delete(claims, name)
		return claims
	}
	claims[name] = value
	return claims
}
