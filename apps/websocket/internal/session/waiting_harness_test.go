package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func expiredTokenError(t *testing.T) error {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":%q}]}`, base64.RawURLEncoding.EncodeToString(public))
	verifier, err := jwtverify.New(jwtverify.Config{JWKS: []byte(jwks), Issuer: "issuer", Audience: "audience"})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "issuer", "aud": "audience", "sub": "u1", "tid": "t1",
		"iat": time.Now().Add(-2 * time.Hour).Unix(), "exp": time.Now().Add(-time.Hour).Unix(),
	})
	token.Header["kid"] = "k1"
	signed, err := token.SignedString(private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = verifier.Verify(signed)
	if jwtverify.ReasonOf(err) != jwtverify.ReasonExpired {
		t.Fatalf("expected an expired token error, got %v", err)
	}
	return err
}

func (h *harness) readyClientWith(token string) *testClient {
	h.t.Helper()
	tc := h.dial(true)
	tc.auth(token)
	if f := tc.nextFrame(); f.GetReady() == nil {
		h.t.Fatalf("first frame = %v, want ready", f)
	}
	return tc
}

func (tc *testClient) expectAuthRequired(want chatimv1.AuthRequiredReason) {
	tc.t.Helper()
	f := tc.nextFrame()
	if f.GetAuthRequired() == nil || f.GetAuthRequired().GetReason() != want {
		tc.t.Fatalf("frame = %v, want auth_required %v", f, want)
	}
}

func (tc *testClient) expectRetryLater() time.Duration {
	tc.t.Helper()
	f := tc.nextFrame()
	if f.GetRetryLater() == nil {
		tc.t.Fatalf("frame = %v, want retry_later", f)
	}
	return time.Duration(f.GetRetryLater().GetRetryAfterMs()) * time.Millisecond
}

func (h *harness) sessionMetric(name, key, value string) float64 {
	h.t.Helper()
	for _, s := range h.server.Sources() {
		if s.Name == name && s.Labels[key] == value {
			return s.Read()
		}
	}
	h.t.Fatalf("no metric %s{%s=%q}", name, key, value)
	return 0
}

func (h *harness) authCount(result string) float64 {
	return h.sessionMetric("auth_total", "result", result)
}

func (h *harness) unauth() int64 { return h.server.stats.unauth.Load() }
