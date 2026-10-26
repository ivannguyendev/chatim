package signing_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

const (
	testIssuer   = "chatim-dev"
	testAudience = "chatim-websocket"
)

func newKey(t testing.TB) signing.Key {
	t.Helper()
	k, err := signing.Generate(rand.Reader)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return k
}

func newSigner(t testing.TB, k signing.Key) *signing.Signer {
	t.Helper()
	s, err := signing.NewSigner(k, signing.TokenConfig{Issuer: testIssuer, Audience: testAudience, TTL: 15 * time.Minute})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func newVerifier(t testing.TB, k signing.Key, now time.Time) *jwtverify.Verifier {
	t.Helper()
	set, err := signing.JWKS([]signing.PublicKey{k.PublicKey()})
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}
	v, err := jwtverify.New(jwtverify.Config{JWKS: set, Issuer: testIssuer, Audience: testAudience, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("jwtverify.New: %v", err)
	}
	return v
}

func TestGenerateMakesDistinctPrefixedKeyIDs(t *testing.T) {
	a, b := newKey(t), newKey(t)
	if !strings.HasPrefix(a.ID, "k-") || len(a.ID) != 14 {
		t.Fatalf("kid = %q, want k- plus 12 characters", a.ID)
	}
	if a.ID == b.ID {
		t.Fatal("two generated keys share a kid")
	}
}

func TestSignedTokenPassesTheExistingVerifier(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	k := newKey(t)
	token, expires, err := newSigner(t, k).Sign(signing.Claims{Tenant: "shop", Subject: "a7k2", Session: "s9q", IssuedAt: now})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !expires.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires = %v, want iat + 15m", expires)
	}
	claims, err := newVerifier(t, k, now.Add(time.Minute)).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Tenant != "shop" || claims.User != "a7k2" {
		t.Fatalf("claims = %+v, want tenant shop, user a7k2", claims)
	}
}

func TestSignedTokenCarriesSessionIssuedAtAndKeyID(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	k := newKey(t)
	token, _, err := newSigner(t, k).Sign(signing.Claims{Tenant: "shop", Subject: "a7k2", Session: "s9q", IssuedAt: now})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sid"] != "s9q" || claims["iat"] != float64(now.Unix()) {
		t.Fatalf("claims = %v, want sid s9q and iat %d", claims, now.Unix())
	}
	if parsed.Header["kid"] != k.ID || parsed.Header["typ"] != "JWT" || parsed.Header["alg"] != "EdDSA" {
		t.Fatalf("header = %v, want kid %s, typ JWT, alg EdDSA", parsed.Header, k.ID)
	}
}

func TestTamperedTokenIsRejected(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	k := newKey(t)
	token, _, err := newSigner(t, k).Sign(signing.Claims{Tenant: "shop", Subject: "a7k2", Session: "s9q", IssuedAt: now})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(token, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var body map[string]any
	_ = json.Unmarshal(payload, &body)
	body["sub"] = "intruder"
	forged, _ := json.Marshal(body)
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)
	if _, err := newVerifier(t, k, now).Verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("Verify accepted a token with a changed subject")
	}
}

func TestNewSignerRejectsMissingConfig(t *testing.T) {
	k := newKey(t)
	cases := map[string]signing.TokenConfig{
		"issuer":   {Audience: testAudience, TTL: time.Minute},
		"audience": {Issuer: testIssuer, TTL: time.Minute},
		"ttl":      {Issuer: testIssuer, Audience: testAudience},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := signing.NewSigner(k, cfg); err == nil {
				t.Fatal("NewSigner accepted an incomplete config")
			}
		})
	}
}
