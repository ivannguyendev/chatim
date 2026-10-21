package jwtverify_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

func TestNewRefusesBadKeySets(t *testing.T) {
	key := newTestKey(t, testKID)
	with := func(name string, value any) map[string]any {
		jwk := publicJWK(key)
		if value == nil {
			delete(jwk, name)
			return jwk
		}
		jwk[name] = value
		return jwk
	}
	cases := []struct {
		name string
		jwks []byte
	}{
		{"empty file", nil},
		{"not json", []byte("{")},
		{"no keys field", []byte(`{}`)},
		{"empty keys", []byte(`{"keys":[]}`)},
		{"private key", keySet(t, with("d", b64(key.priv.Seed())))},
		{"duplicate kid", keySet(t, publicJWK(key), publicJWK(newTestKey(t, testKID)))},
		{"kty RSA", keySet(t, with("kty", "RSA"))},
		{"kty EC", keySet(t, with("kty", "EC"))},
		{"crv X25519", keySet(t, with("crv", "X25519"))},
		{"x 31 bytes", keySet(t, with("x", b64(key.pub[:31])))},
		{"x 33 bytes", keySet(t, with("x", b64(append([]byte{0}, key.pub...))))},
		{"x not base64url", keySet(t, with("x", "!!"))},
		{"x missing", keySet(t, with("x", nil))},
		{"x padded", keySet(t, with("x", b64(key.pub)+"="))},
		{"alg RS256", keySet(t, with("alg", "RS256"))},
		{"use enc", keySet(t, with("use", "enc"))},
		{"kid missing", keySet(t, with("kid", nil))},
		{"kid empty", keySet(t, with("kid", ""))},
		{"kid numeric", keySet(t, with("kid", 1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := jwtverify.New(testConfig(tc.jwks))
			if !errors.Is(err, jwtverify.ErrInvalidKeySet) {
				t.Fatalf("New err = %v; want ErrInvalidKeySet", err)
			}
		})
	}
}

func TestNewAcceptsAlgAndUseWhenRight(t *testing.T) {
	key := newTestKey(t, testKID)
	jwk := publicJWK(key)
	jwk["alg"] = "EdDSA"
	jwk["use"] = "sig"
	v, err := jwtverify.New(testConfig(keySet(t, jwk)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := v.Verify(signEdDSA(t, key, validClaims())); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestNewRefusesBadConfig(t *testing.T) {
	jwks := keySet(t, publicJWK(newTestKey(t, testKID)))
	cases := []struct {
		name   string
		mutate func(*jwtverify.Config)
	}{
		{"no issuer", func(c *jwtverify.Config) { c.Issuer = "" }},
		{"no audience", func(c *jwtverify.Config) { c.Audience = "" }},
		{"negative leeway", func(c *jwtverify.Config) { c.Leeway = -time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(jwks)
			tc.mutate(&cfg)
			if _, err := jwtverify.New(cfg); !errors.Is(err, jwtverify.ErrInvalidConfig) {
				t.Fatalf("New err = %v; want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewUsesTheWallClockByDefault(t *testing.T) {
	key := newTestKey(t, testKID)
	cfg := testConfig(keySet(t, publicJWK(key)))
	cfg.Now = nil
	v, err := jwtverify.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	claims := withClaim("exp", time.Now().Add(time.Hour).Unix())
	claims["nbf"] = time.Now().Add(-time.Minute).Unix()
	if _, err := v.Verify(signEdDSA(t, key, claims)); err != nil {
		t.Fatalf("Verify against the wall clock: %v", err)
	}
}

func TestLoadFileReadsTheKeySet(t *testing.T) {
	key := newTestKey(t, testKID)
	path := filepath.Join(t.TempDir(), "jwks.json")
	if err := os.WriteFile(path, keySet(t, publicJWK(key)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	v, err := jwtverify.LoadFile(path, testConfig(nil))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if _, err := v.Verify(signEdDSA(t, key, validClaims())); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := jwtverify.LoadFile(filepath.Join(t.TempDir(), "missing.json"), testConfig(nil)); err == nil {
		t.Fatal("LoadFile of a missing file succeeded")
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := jwtverify.LoadFile(empty, testConfig(nil)); !errors.Is(err, jwtverify.ErrInvalidKeySet) {
		t.Fatalf("LoadFile empty err = %v; want ErrInvalidKeySet", err)
	}
}
