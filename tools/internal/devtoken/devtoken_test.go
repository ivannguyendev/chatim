package devtoken_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func verifierFor(t *testing.T, key devtoken.KeyPair) *jwtverify.Verifier {
	t.Helper()
	jwks, err := key.PublicJWKS()
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}
	v, err := jwtverify.New(jwtverify.Config{
		JWKS:     jwks,
		Issuer:   "chatim-dev",
		Audience: "chatim-websocket",
		Leeway:   30 * time.Second,
		Now:      func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("jwtverify.New: %v", err)
	}
	return v
}

func testClaims() devtoken.Claims {
	return devtoken.Claims{
		Issuer:   "chatim-dev",
		Audience: "chatim-websocket",
		Subject:  "alice",
		Tenant:   "acme",
		IssuedAt: testNow,
		TTL:      time.Hour,
	}
}

func TestSignedTokensPassVerify(t *testing.T) {
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	token, err := key.Sign(testClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := verifierFor(t, key).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Tenant != "acme" || got.User != "alice" || !got.ExpiresAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("Verify = %+v", got)
	}
	if len(got.Session) != 26 || !got.IssuedAt.Equal(testNow) {
		t.Fatalf("session %q, issued %v; want a 26-character session and the issue time", got.Session, got.IssuedAt)
	}
	again, err := key.Sign(testClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	second, err := verifierFor(t, key).Verify(again)
	if err != nil || second.Session == got.Session {
		t.Fatalf("second token session %q (%v); want a new random session", second.Session, err)
	}
	claims := testClaims()
	claims.Session = "phone-1"
	fixed, err := key.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if third, err := verifierFor(t, key).Verify(fixed); err != nil || third.Session != "phone-1" {
		t.Fatalf("given session = %q (%v); want phone-1", third.Session, err)
	}
}

func TestNegativeTTLMintsAnExpiredToken(t *testing.T) {
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	claims := testClaims()
	claims.TTL = -time.Hour
	token, err := key.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := verifierFor(t, key).Verify(token); jwtverify.ReasonOf(err) != jwtverify.ReasonExpired {
		t.Fatalf("Verify err = %v; want expired", err)
	}
}

func TestPrivateJWKRoundTripsAndStaysOutOfTheVerifier(t *testing.T) {
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	private, err := key.PrivateJWK()
	if err != nil {
		t.Fatalf("PrivateJWK: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(private, &fields); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for name, want := range map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": "dev-1", "alg": "EdDSA", "use": "sig"} {
		if fields[name] != want {
			t.Fatalf("private JWK %s = %v; want %s", name, fields[name], want)
		}
	}
	if d, _ := fields["d"].(string); d == "" {
		t.Fatal("private JWK has no d")
	}
	loaded, err := devtoken.ParsePrivateJWK(private)
	if err != nil {
		t.Fatalf("ParsePrivateJWK: %v", err)
	}
	token, err := loaded.Sign(testClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := verifierFor(t, key).Verify(token); err != nil {
		t.Fatalf("Verify with the loaded key: %v", err)
	}
	set := append(append([]byte(`{"keys":[`), private...), ']', '}')
	if _, err := jwtverify.New(jwtverify.Config{JWKS: set, Issuer: "i", Audience: "a"}); !errors.Is(err, jwtverify.ErrInvalidKeySet) {
		t.Fatalf("verifier accepted a private key: %v", err)
	}
}

func TestRefusesBadInput(t *testing.T) {
	if _, err := devtoken.Generate(""); err == nil {
		t.Fatal("Generate with an empty kid succeeded")
	}
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	claims := testClaims()
	claims.TTL = 0
	if _, err := key.Sign(claims); err == nil {
		t.Fatal("Sign with a zero ttl succeeded")
	}
	for name, data := range map[string]string{
		"not json":   "{",
		"no d":       `{"kty":"OKP","crv":"Ed25519","kid":"k","x":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
		"wrong kty":  `{"kty":"RSA","crv":"Ed25519","kid":"k","d":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
		"short d":    `{"kty":"OKP","crv":"Ed25519","kid":"k","d":"AAAA"}`,
		"no kid":     `{"kty":"OKP","crv":"Ed25519","d":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
		"mismatch x": `{"kty":"OKP","crv":"Ed25519","kid":"k","x":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","d":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
	} {
		if _, err := devtoken.ParsePrivateJWK([]byte(data)); err == nil {
			t.Fatalf("ParsePrivateJWK %s succeeded", name)
		}
	}
}
