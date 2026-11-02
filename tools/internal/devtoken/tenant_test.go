package devtoken_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

var tenantAlgorithms = []string{"EdDSA", "ES256", "RS256"}

func publicKeyFromJWK(t *testing.T, raw []byte) (string, crypto.PublicKey) {
	t.Helper()
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("public JWK is not a flat JSON object of strings: %v", err)
	}
	b64 := func(name string) []byte {
		b, err := base64.RawURLEncoding.Strict().DecodeString(fields[name])
		if err != nil || len(b) == 0 {
			t.Fatalf("JWK field %s = %q is not base64url", name, fields[name])
		}
		return b
	}
	switch fields["kty"] {
	case "OKP":
		x := b64("x")
		if fields["crv"] != "Ed25519" || len(x) != ed25519.PublicKeySize {
			t.Fatalf("OKP JWK = %v", fields)
		}
		return fields["alg"], ed25519.PublicKey(x)
	case "EC":
		x, y := b64("x"), b64("y")
		if fields["crv"] != "P-256" || len(x) != 32 || len(y) != 32 {
			t.Fatalf("EC JWK = %v", fields)
		}
		key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			t.Fatalf("EC point: %v", err)
		}
		return fields["alg"], key
	case "RSA":
		e := new(big.Int).SetBytes(b64("e"))
		return fields["alg"], &rsa.PublicKey{N: new(big.Int).SetBytes(b64("n")), E: int(e.Int64())}
	}
	t.Fatalf("unknown kty in %v", fields)
	return "", nil
}

func verifyTenantToken(t *testing.T, publicJWK []byte, token string) (jwt.MapClaims, map[string]any) {
	t.Helper()
	alg, key := publicKeyFromJWK(t, publicJWK)
	claims := jwt.MapClaims{}
	parsed, err := jwt.NewParser(
		jwt.WithValidMethods([]string{alg}),
		jwt.WithAudience("chatim"),
		jwt.WithIssuer("acme-backend"),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return testNow }),
	).ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return key, nil })
	if err != nil {
		t.Fatalf("verify %s token: %v", alg, err)
	}
	return claims, parsed.Header
}

func tenantClaims() devtoken.TenantClaims {
	return devtoken.TenantClaims{
		Issuer:   "acme-backend",
		Audience: "chatim",
		Subject:  "ha",
		Tenant:   "acme",
		Name:     "Hà",
		Picture:  "https://cdn.acme.example/ha.png",
		IssuedAt: testNow,
		TTL:      2 * time.Minute,
	}
}

func TestTenantKeySignsTokensVerifiedByItsPublicJWK(t *testing.T) {
	for _, alg := range tenantAlgorithms {
		t.Run(alg, func(t *testing.T) {
			key, err := devtoken.GenerateTenantKey(alg, "k1")
			if err != nil {
				t.Fatalf("GenerateTenantKey: %v", err)
			}
			public, err := key.PublicJWK()
			if err != nil {
				t.Fatalf("PublicJWK: %v", err)
			}
			token, err := key.Sign(tenantClaims())
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			claims, header := verifyTenantToken(t, public, token)
			if header["kid"] != "k1" || header["alg"] != alg {
				t.Fatalf("header = %v", header)
			}
			if claims["sub"] != "ha" || claims["tid"] != "acme" || claims["name"] != "Hà" || claims["picture"] != "https://cdn.acme.example/ha.png" {
				t.Fatalf("claims = %v", claims)
			}
			if claims["iat"] != float64(testNow.Unix()) || claims["exp"] != float64(testNow.Add(2*time.Minute).Unix()) {
				t.Fatalf("times = %v %v", claims["iat"], claims["exp"])
			}
		})
	}
}

func TestTenantPublicJWKCarriesOnlyPublicFields(t *testing.T) {
	want := map[string][]string{
		"EdDSA": {"alg", "crv", "kid", "kty", "x"},
		"ES256": {"alg", "crv", "kid", "kty", "x", "y"},
		"RS256": {"alg", "e", "kid", "kty", "n"},
	}
	for _, alg := range tenantAlgorithms {
		key, err := devtoken.GenerateTenantKey(alg, "k1")
		if err != nil {
			t.Fatalf("GenerateTenantKey(%s): %v", alg, err)
		}
		public, err := key.PublicJWK()
		if err != nil {
			t.Fatalf("PublicJWK: %v", err)
		}
		var fields map[string]string
		if err := json.Unmarshal(public, &fields); err != nil {
			t.Fatalf("PublicJWK JSON: %v", err)
		}
		if got := slices.Sorted(maps.Keys(fields)); !slices.Equal(got, want[alg]) {
			t.Fatalf("%s public JWK fields = %v, want %v", alg, got, want[alg])
		}
		if fields["alg"] != alg || fields["kid"] != "k1" {
			t.Fatalf("%s public JWK = %v", alg, fields)
		}
	}
}

func TestTenantRSAKeyIs2048Bits(t *testing.T) {
	key, err := devtoken.GenerateTenantKey("RS256", "k2")
	if err != nil {
		t.Fatalf("GenerateTenantKey: %v", err)
	}
	public, err := key.PublicJWK()
	if err != nil {
		t.Fatalf("PublicJWK: %v", err)
	}
	_, parsed := publicKeyFromJWK(t, public)
	if rsaKey := parsed.(*rsa.PublicKey); rsaKey.N.BitLen() != 2048 || rsaKey.E != 65537 {
		t.Fatalf("RSA key = %d bits, e %d", rsaKey.N.BitLen(), rsaKey.E)
	}
}

func TestTenantTokenOmitsEmptyOptionalClaims(t *testing.T) {
	key, err := devtoken.GenerateTenantKey("EdDSA", "k1")
	if err != nil {
		t.Fatalf("GenerateTenantKey: %v", err)
	}
	public, _ := key.PublicJWK()
	c := tenantClaims()
	c.Tenant, c.Name, c.Picture = "", "", ""
	token, err := key.Sign(c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, _ := verifyTenantToken(t, public, token)
	for _, name := range []string{"tid", "name", "picture", "sid"} {
		if _, ok := claims[name]; ok {
			t.Fatalf("claim %s present in %v", name, claims)
		}
	}
}

func TestTenantSignKeyIDOverride(t *testing.T) {
	key, err := devtoken.GenerateTenantKey("RS256", "k2")
	if err != nil {
		t.Fatalf("GenerateTenantKey: %v", err)
	}
	public, _ := key.PublicJWK()
	c := tenantClaims()
	c.KeyID = "k1"
	token, err := key.Sign(c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, header := verifyTenantToken(t, public, token); header["kid"] != "k1" {
		t.Fatalf("kid = %v, want the override k1", header["kid"])
	}
}

func TestTenantPrivateJWKRoundTrip(t *testing.T) {
	for _, alg := range tenantAlgorithms {
		t.Run(alg, func(t *testing.T) {
			key, err := devtoken.GenerateTenantKey(alg, "k1")
			if err != nil {
				t.Fatalf("GenerateTenantKey: %v", err)
			}
			private, err := key.PrivateJWK()
			if err != nil {
				t.Fatalf("PrivateJWK: %v", err)
			}
			loaded, err := devtoken.ParseTenantKey(private)
			if err != nil {
				t.Fatalf("ParseTenantKey: %v", err)
			}
			if loaded.KeyID != "k1" || loaded.Algorithm != alg {
				t.Fatalf("loaded = %s %s", loaded.KeyID, loaded.Algorithm)
			}
			public, _ := key.PublicJWK()
			reloaded, _ := loaded.PublicJWK()
			if string(public) != string(reloaded) {
				t.Fatalf("public JWK changed after round trip:\n%s\n%s", public, reloaded)
			}
			token, err := loaded.Sign(tenantClaims())
			if err != nil {
				t.Fatalf("Sign with loaded key: %v", err)
			}
			verifyTenantToken(t, public, token)
		})
	}
}

func TestTenantKeyRefusesBadInput(t *testing.T) {
	if _, err := devtoken.GenerateTenantKey("HS256", "k1"); !errors.Is(err, devtoken.ErrInvalidKey) {
		t.Fatalf("GenerateTenantKey(HS256) = %v", err)
	}
	if _, err := devtoken.GenerateTenantKey("ES256", ""); !errors.Is(err, devtoken.ErrInvalidKey) {
		t.Fatalf("GenerateTenantKey without kid = %v", err)
	}
	key, err := devtoken.GenerateTenantKey("ES256", "k1")
	if err != nil {
		t.Fatalf("GenerateTenantKey: %v", err)
	}
	c := tenantClaims()
	c.TTL = 0
	if _, err := key.Sign(c); err == nil {
		t.Fatal("Sign accepted a zero TTL")
	}
	public, _ := key.PublicJWK()
	edPrivate, _ := devtoken.Generate("k1")
	oldFormat, _ := edPrivate.PrivateJWK()
	for name, data := range map[string][]byte{
		"not json":           []byte("{"),
		"public only":        public,
		"alg not supported":  []byte(`{"kty":"oct","kid":"k1","alg":"HS256","k":"AAAA"}`),
		"alg does not match": []byte(`{"kty":"OKP","crv":"Ed25519","kid":"k1","alg":"ES256","d":"` + base64.RawURLEncoding.EncodeToString(make([]byte, 32)) + `"}`),
		"point mismatch":     mismatchedPoint(t, public),
		"missing kid":        []byte(`{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","d":"` + base64.RawURLEncoding.EncodeToString(make([]byte, 32)) + `"}`),
	} {
		if _, err := devtoken.ParseTenantKey(data); !errors.Is(err, devtoken.ErrInvalidKey) {
			t.Fatalf("ParseTenantKey(%s) = %v", name, err)
		}
	}
	if loaded, err := devtoken.ParseTenantKey(oldFormat); err != nil || loaded.Algorithm != "EdDSA" {
		t.Fatalf("ParseTenantKey(dev key) = %+v, %v", loaded, err)
	}
}

func mismatchedPoint(t *testing.T, public []byte) []byte {
	t.Helper()
	other, err := devtoken.GenerateTenantKey("ES256", "k1")
	if err != nil {
		t.Fatalf("GenerateTenantKey: %v", err)
	}
	private, _ := other.PrivateJWK()
	var fields, publicFields map[string]string
	_ = json.Unmarshal(private, &fields)
	_ = json.Unmarshal(public, &publicFields)
	fields["x"], fields["y"] = publicFields["x"], publicFields["y"]
	out, _ := json.Marshal(fields)
	return out
}
