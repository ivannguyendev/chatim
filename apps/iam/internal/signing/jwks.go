package signing

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrKeyConflict = errors.New("key id already used by another key")

type jsonWebKey struct {
	KeyType string          `json:"kty"`
	Curve   string          `json:"crv"`
	KeyID   string          `json:"kid"`
	X       string          `json:"x"`
	Alg     string          `json:"alg,omitempty"`
	Use     string          `json:"use,omitempty"`
	D       json.RawMessage `json:"d,omitempty"`
}

type jsonWebKeySet struct {
	Keys []jsonWebKey `json:"keys"`
}

func JWKS(keys []PublicKey) ([]byte, error) {
	return Merge(nil, keys)
}

func Merge(existing []byte, keys []PublicKey) ([]byte, error) {
	set := jsonWebKeySet{Keys: []jsonWebKey{}}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &set); err != nil {
			return nil, fmt.Errorf("read existing key set: %w", err)
		}
	}
	index := make(map[string]string, len(set.Keys)+len(keys))
	for _, k := range set.Keys {
		if len(k.D) > 0 {
			return nil, errors.New("existing key set contains private key material")
		}
		if _, dup := index[k.KeyID]; dup {
			return nil, fmt.Errorf("%w: %s", ErrKeyConflict, k.KeyID)
		}
		index[k.KeyID] = k.X
	}
	for _, k := range keys {
		jwk := publicJWK(k)
		if x, seen := index[k.ID]; seen {
			if x != jwk.X {
				return nil, fmt.Errorf("%w: %s", ErrKeyConflict, k.ID)
			}
			continue
		}
		index[k.ID] = jwk.X
		set.Keys = append(set.Keys, jwk)
	}
	return json.Marshal(set)
}

func publicJWK(k PublicKey) jsonWebKey {
	return jsonWebKey{
		KeyType: "OKP",
		Curve:   "Ed25519",
		KeyID:   k.ID,
		X:       base64.RawURLEncoding.EncodeToString(k.Key),
		Alg:     "EdDSA",
		Use:     "sig",
	}
}
