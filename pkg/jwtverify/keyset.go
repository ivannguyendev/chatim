package jwtverify

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type jsonWebKey struct {
	KeyType string          `json:"kty"`
	Curve   string          `json:"crv"`
	KeyID   string          `json:"kid"`
	X       string          `json:"x"`
	D       json.RawMessage `json:"d"`
	Alg     *string         `json:"alg"`
	Use     *string         `json:"use"`
}

type jsonWebKeySet struct {
	Keys []jsonWebKey `json:"keys"`
}

func parseKeySet(data []byte, allowEmpty bool) (map[string]ed25519.PublicKey, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrInvalidKeySet)
	}
	var set jsonWebKeySet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("%w: not a JWKS document", ErrInvalidKeySet)
	}
	if len(set.Keys) == 0 && !allowEmpty {
		return nil, fmt.Errorf("%w: no keys", ErrInvalidKeySet)
	}
	keys := make(map[string]ed25519.PublicKey, len(set.Keys))
	for i, jwk := range set.Keys {
		pub, err := publicKey(jwk)
		if err != nil {
			return nil, fmt.Errorf("%w: key %d: %s", ErrInvalidKeySet, i, err.Error())
		}
		if _, dup := keys[jwk.KeyID]; dup {
			return nil, fmt.Errorf("%w: key %d: duplicate kid", ErrInvalidKeySet, i)
		}
		keys[jwk.KeyID] = pub
	}
	return keys, nil
}

func publicKey(jwk jsonWebKey) (ed25519.PublicKey, error) {
	switch {
	case len(jwk.D) > 0:
		return nil, errors.New("private key material present")
	case jwk.KeyID == "":
		return nil, errors.New("kid required")
	case jwk.KeyType != "OKP" || jwk.Curve != "Ed25519":
		return nil, errors.New("only kty OKP with crv Ed25519 is accepted")
	case jwk.Alg != nil && *jwk.Alg != "EdDSA":
		return nil, errors.New("alg must be EdDSA")
	case jwk.Use != nil && *jwk.Use != "sig":
		return nil, errors.New("use must be sig")
	}
	x, err := base64.RawURLEncoding.Strict().DecodeString(jwk.X)
	if err != nil || len(x) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("x must be %d base64url bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(x), nil
}
