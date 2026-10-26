package dpop_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/ivannguyendev/chatim/apps/iam/internal/dpop"
)

const tokenPath = "/realms/shop/protocol/openid-connect/token"

var now = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

type proofSpec struct {
	alg    jose.SignatureAlgorithm
	key    crypto.Signer
	typ    string
	embed  bool
	claims map[string]any
}

func defaultClaims() map[string]any {
	return map[string]any{
		"jti": "jti-1",
		"htm": "POST",
		"htu": "https://iam.example.com" + tokenPath,
		"iat": now.Unix(),
	}
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sign(t *testing.T, spec proofSpec) string {
	t.Helper()
	opts := (&jose.SignerOptions{EmbedJWK: spec.embed}).WithType(jose.ContentType(spec.typ))
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: spec.alg, Key: spec.key}, opts)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	payload, _ := json.Marshal(spec.claims)
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	out, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("CompactSerialize: %v", err)
	}
	return out
}

func validSpec(t *testing.T) proofSpec {
	return proofSpec{alg: jose.ES256, key: ecKey(t), typ: "dpop+jwt", embed: true, claims: defaultClaims()}
}

func newVerifier(t *testing.T, publicURL string) *dpop.Verifier {
	t.Helper()
	v, err := dpop.NewVerifier(publicURL, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func thumbprintOf(t *testing.T, key crypto.PublicKey) string {
	t.Helper()
	jwk := jose.JSONWebKey{Key: key}
	sum, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(sum)
}

func TestVerifyAcceptsES256AndEdDSAProofs(t *testing.T) {
	v := newVerifier(t, "https://iam.example.com")
	ec := ecKey(t)
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	for name, spec := range map[string]proofSpec{
		"ES256": {alg: jose.ES256, key: ec, typ: "dpop+jwt", embed: true, claims: defaultClaims()},
		"EdDSA": {alg: jose.EdDSA, key: ed, typ: "dpop+jwt", embed: true, claims: defaultClaims()},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := v.Verify(sign(t, spec), "POST", tokenPath)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if want := thumbprintOf(t, spec.key.Public()); got.Thumbprint != want || got.JTI != "jti-1" {
				t.Fatalf("proof = %+v, want thumbprint %s and jti jti-1", got, want)
			}
		})
	}
}

func TestThumbprintMatchesRFC7638Example(t *testing.T) {
	const rfcKey = `{"kty":"RSA","n":"0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw","e":"AQAB","alg":"RS256","kid":"2011-04-29"}`
	var jwk jose.JSONWebKey
	if err := json.Unmarshal([]byte(rfcKey), &jwk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := dpop.Thumbprint(&jwk)
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	if got != "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs" {
		t.Fatalf("thumbprint = %s, want the RFC 7638 value", got)
	}
}

func TestVerifyRejectsBadProofs(t *testing.T) {
	v := newVerifier(t, "https://iam.example.com")
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	mutate := func(edit func(*proofSpec)) proofSpec {
		s := validSpec(t)
		edit(&s)
		return s
	}
	withClaim := func(name string, value any) proofSpec {
		return mutate(func(s *proofSpec) {
			if value == nil {
				delete(s.claims, name)
				return
			}
			s.claims[name] = value
		})
	}
	cases := map[string]string{
		"wrong typ":        sign(t, mutate(func(s *proofSpec) { s.typ = "JWT" })),
		"no embedded jwk":  sign(t, mutate(func(s *proofSpec) { s.embed = false })),
		"rsa algorithm":    sign(t, mutate(func(s *proofSpec) { s.alg = jose.RS256; s.key = rsaKey })),
		"wrong method":     sign(t, withClaim("htm", "GET")),
		"other host":       sign(t, withClaim("htu", "https://evil.example.com"+tokenPath)),
		"other path":       sign(t, withClaim("htu", "https://iam.example.com/realms/other/protocol/openid-connect/token")),
		"trailing slash":   sign(t, withClaim("htu", "https://iam.example.com"+tokenPath+"/")),
		"iat too old":      sign(t, withClaim("iat", now.Add(-61*time.Second).Unix())),
		"iat too new":      sign(t, withClaim("iat", now.Add(61*time.Second).Unix())),
		"missing jti":      sign(t, withClaim("jti", nil)),
		"long jti":         sign(t, withClaim("jti", strings.Repeat("j", 65))),
		"missing iat":      sign(t, withClaim("iat", nil)),
		"string iat":       sign(t, withClaim("iat", "now")),
		"missing htu":      sign(t, withClaim("htu", nil)),
		"not a jws":        "not-a-proof",
		"empty":            "",
		"oversized":        strings.Repeat("a", dpop.MaxProofLength+1),
		"tampered payload": tamper(sign(t, validSpec(t))),
		"none algorithm":   noneProof(),
	}
	for name, proof := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(proof, "POST", tokenPath); !errors.Is(err, dpop.ErrInvalidProof) {
				t.Fatalf("Verify = %v, want ErrInvalidProof", err)
			}
		})
	}
}

func TestVerifyRejectsAProofCarryingAPrivateKey(t *testing.T) {
	key := ecKey(t)
	private := jose.JSONWebKey{Key: key, Algorithm: string(jose.ES256)}
	header := map[string]any{"alg": "ES256", "typ": "dpop+jwt", "jwk": private}
	h, _ := json.Marshal(header)
	if !strings.Contains(string(h), `"d":`) {
		t.Fatal("test setup: header jwk has no private part")
	}
	signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("dpop+jwt").WithHeader("jwk", private))
	payload, _ := json.Marshal(defaultClaims())
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Skipf("go-jose refuses to embed a private jwk: %v", err)
	}
	proof, _ := obj.CompactSerialize()
	if _, err := newVerifier(t, "https://iam.example.com").Verify(proof, "POST", tokenPath); !errors.Is(err, dpop.ErrInvalidProof) {
		t.Fatalf("Verify = %v, want ErrInvalidProof", err)
	}
}

func TestVerifyNormalizesTheTargetURI(t *testing.T) {
	ok := map[string]struct{ publicURL, htu, path string }{
		"upper case host":  {"https://iam.example.com", "https://IAM.Example.COM" + tokenPath, tokenPath},
		"explicit 443":     {"https://iam.example.com", "https://iam.example.com:443" + tokenPath, tokenPath},
		"query ignored":    {"https://iam.example.com", "https://iam.example.com" + tokenPath + "?x=1#f", tokenPath},
		"public prefix":    {"https://edge.example.com/auth/", "https://edge.example.com/auth" + tokenPath, tokenPath},
		"encoded tenant":   {"https://iam.example.com", "https://iam.example.com/realms/sh%6Fp/protocol/openid-connect/token", tokenPath},
		"http default 80":  {"http://127.0.0.1:8083", "http://127.0.0.1:8083" + tokenPath, tokenPath},
		"scheme lowercase": {"https://iam.example.com", "HTTPS://iam.example.com" + tokenPath, tokenPath},
	}
	for name, c := range ok {
		t.Run(name, func(t *testing.T) {
			spec := validSpec(t)
			spec.claims["htu"] = c.htu
			if _, err := newVerifier(t, c.publicURL).Verify(sign(t, spec), "POST", c.path); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
	t.Run("missing public prefix", func(t *testing.T) {
		spec := validSpec(t)
		spec.claims["htu"] = "https://edge.example.com" + tokenPath
		if _, err := newVerifier(t, "https://edge.example.com/auth").Verify(sign(t, spec), "POST", tokenPath); !errors.Is(err, dpop.ErrInvalidProof) {
			t.Fatalf("Verify = %v, want ErrInvalidProof", err)
		}
	})
}

func TestNewVerifierRejectsBadPublicURL(t *testing.T) {
	for _, u := range []string{"", "iam.example.com", "ftp://iam.example.com", "https://", "https://iam.example.com?x=1"} {
		if _, err := dpop.NewVerifier(u, time.Minute, time.Now); err == nil {
			t.Fatalf("NewVerifier(%q) accepted a bad public url", u)
		}
	}
}

func tamper(proof string) string {
	parts := strings.Split(proof, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(payload, &claims)
	claims["jti"] = "jti-forged"
	forged, _ := json.Marshal(claims)
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)
	return strings.Join(parts, ".")
}

func noneProof() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"dpop+jwt"}`))
	payload, _ := json.Marshal(defaultClaims())
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}
