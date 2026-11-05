package trust_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

func fakeModulus(t *testing.T, bits int) string {
	t.Helper()
	n := make([]byte, (bits+7)/8)
	if _, err := rand.Read(n); err != nil {
		t.Fatalf("rand: %v", err)
	}
	n[0] |= 0x80 >> ((8 - bits%8) % 8)
	n[0] &= 0xff >> ((8 - bits%8) % 8)
	n[len(n)-1] |= 1
	return b64(n)
}

func withField(raw []byte, key string, value any) []byte {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	if value == nil {
		delete(m, key)
	} else {
		m[key] = value
	}
	return mustJSON(m)
}

func TestParsePublicJWKAcceptsTheThreeAlgorithms(t *testing.T) {
	k := testKeys()
	for _, tc := range []struct {
		alg  string
		raw  []byte
		want any
	}{
		{"EdDSA", withField(edJWK(k.ed), "kid", "ed1"), k.ed.Public()},
		{"ES256", withField(ecJWK(k.ec), "use", "sig"), &k.ec.PublicKey},
		{"RS256", withField(rsaJWK(&k.rsa.PublicKey), "alg", "RS256"), &k.rsa.PublicKey},
	} {
		t.Run(tc.alg, func(t *testing.T) {
			pub, canonical, err := trust.ParsePublicJWK(tc.alg, tc.raw)
			if err != nil {
				t.Fatalf("ParsePublicJWK: %v", err)
			}
			if !pub.(interface{ Equal(crypto.PublicKey) bool }).Equal(tc.want) {
				t.Fatalf("public key differs")
			}
			var m map[string]any
			if err := json.Unmarshal(canonical, &m); err != nil {
				t.Fatalf("canonical json: %v", err)
			}
			if m["alg"] != tc.alg {
				t.Fatalf("canonical alg = %v; want %s", m["alg"], tc.alg)
			}
			if _, has := m["use"]; has {
				t.Fatalf("canonical holds use: %s", canonical)
			}
			again, twice, err := trust.ParsePublicJWK(tc.alg, canonical)
			if err != nil || string(twice) != string(canonical) || !again.(interface{ Equal(crypto.PublicKey) bool }).Equal(tc.want) {
				t.Fatalf("canonical form does not round trip: %s / %s (%v)", canonical, twice, err)
			}
		})
	}
}

func TestParsePublicJWKKeepsTheKeyID(t *testing.T) {
	_, canonical, err := trust.ParsePublicJWK("EdDSA", withField(edJWK(testKeys().ed), "kid", "ed1"))
	if err != nil {
		t.Fatalf("ParsePublicJWK: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(canonical, &m); err != nil || m["kid"] != "ed1" || len(m) != 5 {
		t.Fatalf("canonical = %s; want kty crv x kid alg", canonical)
	}
}

func TestParsePublicJWKAcceptsRSA2048And4096(t *testing.T) {
	for _, bits := range []int{2048, 4096} {
		raw := mustJSON(map[string]any{"kty": "RSA", "n": fakeModulus(t, bits), "e": "AQAB"})
		pub, _, err := trust.ParsePublicJWK("RS256", raw)
		if err != nil {
			t.Fatalf("%d bits: %v", bits, err)
		}
		if got := pub.(*rsa.PublicKey).N.BitLen(); got != bits {
			t.Fatalf("bit length = %d; want %d", got, bits)
		}
	}
}

func TestParsePublicJWKRefuses(t *testing.T) {
	k := testKeys()
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	p384raw, err := p384.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	ec := ecJWK(k.ec)
	ed := edJWK(k.ed)
	rs := rsaJWK(&k.rsa.PublicKey)
	edPub := k.ed.Public().(ed25519.PublicKey)
	for name, tc := range map[string]struct {
		alg string
		raw []byte
	}{
		"rsa 1024 bits":            {"RS256", mustJSON(map[string]any{"kty": "RSA", "n": fakeModulus(t, 1024), "e": "AQAB"})},
		"rsa 2047 bits":            {"RS256", mustJSON(map[string]any{"kty": "RSA", "n": fakeModulus(t, 2047), "e": "AQAB"})},
		"rsa 4097 bits":            {"RS256", mustJSON(map[string]any{"kty": "RSA", "n": fakeModulus(t, 4097), "e": "AQAB"})},
		"rsa 8192 bits":            {"RS256", mustJSON(map[string]any{"kty": "RSA", "n": fakeModulus(t, 8192), "e": "AQAB"})},
		"rsa exponent 3":           {"RS256", withField(rs, "e", "Aw")},
		"rsa without n":            {"RS256", withField(rs, "n", nil)},
		"rsa private d":            {"RS256", withField(rs, "d", b64(k.rsa.D.Bytes()))},
		"rsa private p":            {"RS256", withField(rs, "p", "AQAB")},
		"rsa private qi":           {"RS256", withField(rs, "qi", "AQAB")},
		"okp private d":            {"EdDSA", withField(ed, "d", b64(k.ed.Seed()))},
		"symmetric k":              {"EdDSA", withField(ed, "k", "AQAB")},
		"ec private d":             {"ES256", withField(ec, "d", "AQAB")},
		"p-384":                    {"ES256", mustJSON(map[string]any{"kty": "EC", "crv": "P-384", "x": b64(p384raw[1:49]), "y": b64(p384raw[49:])})},
		"ec point off the curve":   {"ES256", withField(ec, "y", b64(make([]byte, 32)))},
		"ec x of 31 bytes":         {"ES256", withField(ec, "x", b64(make([]byte, 31)))},
		"jwk alg differs":          {"ES256", withField(ec, "alg", "RS256")},
		"jwk alg not a string":     {"ES256", withField(ec, "alg", 1)},
		"use enc":                  {"EdDSA", withField(ed, "use", "enc")},
		"kty differs from alg":     {"RS256", ed},
		"eddsa with an ec key":     {"EdDSA", ec},
		"x448 curve":               {"EdDSA", withField(ed, "crv", "Ed448")},
		"ed x of 33 bytes":         {"EdDSA", withField(ed, "x", b64(append(edPub, 0)))},
		"x with padding":           {"EdDSA", withField(ed, "x", b64(edPub)+"=")},
		"x in standard base64":     {"EdDSA", withField(ed, "x", "+/"+b64(edPub)[2:])},
		"x not a string":           {"EdDSA", withField(ed, "x", 5)},
		"kid not a string":         {"EdDSA", withField(ed, "kid", 5)},
		"hs256":                    {"HS256", ed},
		"none":                     {"none", ed},
		"not json":                 {"EdDSA", []byte("{")},
		"json array":               {"EdDSA", []byte("[]")},
		"rsa modulus with a big e": {"RS256", withField(rs, "e", b64(big.NewInt(1<<40+1).Bytes()))},
		"empty":                    {"EdDSA", nil},
	} {
		t.Run(name, func(t *testing.T) {
			pub, canonical, err := trust.ParsePublicJWK(tc.alg, tc.raw)
			if err == nil || pub != nil || canonical != nil {
				t.Fatalf("accepted: %s", canonical)
			}
			if !errors.Is(err, trust.ErrInvalidKey) {
				t.Fatalf("err = %v; want ErrInvalidKey", err)
			}
		})
	}
}
