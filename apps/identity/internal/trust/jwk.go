package trust

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

const (
	AlgorithmEdDSA = "EdDSA"
	AlgorithmES256 = "ES256"
	AlgorithmRS256 = "RS256"

	rsaMinBits     = 2048
	rsaMaxBits     = 4096
	rsaExponent    = 65537
	p256CoordBytes = 32
)

var ErrInvalidKey = errors.New("invalid trusted key")

var privateMembers = []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"}

type canonicalJWK struct {
	KeyType string `json:"kty"`
	Curve   string `json:"crv,omitempty"`
	X       string `json:"x,omitempty"`
	Y       string `json:"y,omitempty"`
	N       string `json:"n,omitempty"`
	E       string `json:"e,omitempty"`
	KeyID   string `json:"kid,omitempty"`
	Alg     string `json:"alg"`
}

type jwkMembers map[string]json.RawMessage

func ParsePublicJWK(algorithm string, raw []byte) (crypto.PublicKey, []byte, error) {
	pub, out, err := parsePublicJWK(algorithm, raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrInvalidKey, err.Error())
	}
	canonical, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrInvalidKey, err.Error())
	}
	return pub, canonical, nil
}

func parsePublicJWK(algorithm string, raw []byte) (crypto.PublicKey, canonicalJWK, error) {
	var members jwkMembers
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return nil, canonicalJWK{}, errors.New("not a JSON object")
	}
	for _, name := range privateMembers {
		if _, ok := members[name]; ok {
			return nil, canonicalJWK{}, fmt.Errorf("private member %q present", name)
		}
	}
	out := canonicalJWK{Alg: algorithm}
	var err error
	if out.KeyType, err = members.text("kty", true); err != nil {
		return nil, out, err
	}
	if out.KeyID, err = members.text("kid", false); err != nil {
		return nil, out, err
	}
	if alg, err := members.text("alg", false); err != nil || (alg != "" && alg != algorithm) {
		return nil, out, errors.New("alg must match the declared algorithm")
	}
	if use, err := members.text("use", false); err != nil || (use != "" && use != "sig") {
		return nil, out, errors.New("use must be sig")
	}
	switch algorithm {
	case AlgorithmEdDSA:
		return parseEd25519(members, out)
	case AlgorithmES256:
		return parseP256(members, out)
	case AlgorithmRS256:
		return parseRSA(members, out)
	default:
		return nil, out, errors.New("algorithm must be EdDSA, ES256 or RS256")
	}
}

func parseEd25519(m jwkMembers, out canonicalJWK) (crypto.PublicKey, canonicalJWK, error) {
	if out.KeyType != "OKP" {
		return nil, out, errors.New("EdDSA needs kty OKP")
	}
	crv, err := m.text("crv", true)
	if err != nil || crv != "Ed25519" {
		return nil, out, errors.New("EdDSA needs crv Ed25519")
	}
	x, err := m.bytes("x")
	if err != nil || len(x) != ed25519.PublicKeySize {
		return nil, out, fmt.Errorf("x must be %d base64url bytes", ed25519.PublicKeySize)
	}
	out.Curve, out.X = crv, encode(x)
	return ed25519.PublicKey(x), out, nil
}

func parseP256(m jwkMembers, out canonicalJWK) (crypto.PublicKey, canonicalJWK, error) {
	if out.KeyType != "EC" {
		return nil, out, errors.New("ES256 needs kty EC")
	}
	crv, err := m.text("crv", true)
	if err != nil || crv != "P-256" {
		return nil, out, errors.New("ES256 needs crv P-256")
	}
	x, errX := m.bytes("x")
	y, errY := m.bytes("y")
	if errX != nil || errY != nil || len(x) != p256CoordBytes || len(y) != p256CoordBytes {
		return nil, out, fmt.Errorf("x and y must be %d base64url bytes", p256CoordBytes)
	}
	point := append(append([]byte{4}, x...), y...)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return nil, out, errors.New("point is not on P-256")
	}
	out.Curve, out.X, out.Y = crv, encode(x), encode(y)
	return pub, out, nil
}

func parseRSA(m jwkMembers, out canonicalJWK) (crypto.PublicKey, canonicalJWK, error) {
	if out.KeyType != "RSA" {
		return nil, out, errors.New("RS256 needs kty RSA")
	}
	nRaw, errN := m.bytes("n")
	eRaw, errE := m.bytes("e")
	if errN != nil || errE != nil {
		return nil, out, errors.New("n and e must be base64url")
	}
	n := new(big.Int).SetBytes(nRaw)
	if bits := n.BitLen(); bits < rsaMinBits || bits > rsaMaxBits {
		return nil, out, fmt.Errorf("modulus must be %d to %d bits", rsaMinBits, rsaMaxBits)
	}
	if n.Bit(0) == 0 {
		return nil, out, errors.New("modulus must be odd")
	}
	e := new(big.Int).SetBytes(eRaw)
	if !e.IsInt64() || e.Int64() != rsaExponent {
		return nil, out, fmt.Errorf("e must be %d", rsaExponent)
	}
	out.N, out.E = encode(n.Bytes()), encode(e.Bytes())
	return &rsa.PublicKey{N: n, E: rsaExponent}, out, nil
}

func (m jwkMembers) text(name string, required bool) (string, error) {
	raw, ok := m[name]
	if !ok {
		if required {
			return "", fmt.Errorf("%s required", name)
		}
		return "", nil
	}
	var s string
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if required && s == "" {
		return "", fmt.Errorf("%s required", name)
	}
	return s, nil
}

func (m jwkMembers) bytes(name string) ([]byte, error) {
	s, err := m.text(name, true)
	if err != nil {
		return nil, err
	}
	return base64.RawURLEncoding.Strict().DecodeString(s)
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
