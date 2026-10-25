package devtoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidKey = errors.New("invalid dev key")

type KeyPair struct {
	KeyID   string
	Private ed25519.PrivateKey
}

type Claims struct {
	Issuer   string
	Audience string
	Subject  string
	Tenant   string
	IssuedAt time.Time
	TTL      time.Duration
}

type jwk struct {
	KeyType string `json:"kty"`
	Curve   string `json:"crv"`
	KeyID   string `json:"kid"`
	Alg     string `json:"alg"`
	Use     string `json:"use"`
	X       string `json:"x"`
	D       string `json:"d,omitempty"`
}

func Generate(kid string) (KeyPair, error) {
	if kid == "" {
		return KeyPair{}, fmt.Errorf("%w: kid required", ErrInvalidKey)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return KeyPair{KeyID: kid, Private: private}, nil
}

func ParsePrivateJWK(data []byte) (KeyPair, error) {
	var key jwk
	if err := json.Unmarshal(data, &key); err != nil {
		return KeyPair{}, fmt.Errorf("%w: not a JWK", ErrInvalidKey)
	}
	if key.KeyID == "" || key.KeyType != "OKP" || key.Curve != "Ed25519" {
		return KeyPair{}, fmt.Errorf("%w: want kty OKP, crv Ed25519 and a kid", ErrInvalidKey)
	}
	seed, err := decode(key.D)
	if err != nil || len(seed) != ed25519.SeedSize {
		return KeyPair{}, fmt.Errorf("%w: d must be a %d byte seed", ErrInvalidKey, ed25519.SeedSize)
	}
	private := ed25519.NewKeyFromSeed(seed)
	if key.X != "" {
		x, err := decode(key.X)
		if err != nil || subtle.ConstantTimeCompare(x, private.Public().(ed25519.PublicKey)) != 1 {
			return KeyPair{}, fmt.Errorf("%w: x does not match d", ErrInvalidKey)
		}
	}
	return KeyPair{KeyID: key.KeyID, Private: private}, nil
}

func (k KeyPair) PrivateJWK() ([]byte, error) {
	key := k.publicJWK()
	key.D = encode(k.Private.Seed())
	return json.Marshal(key)
}

func (k KeyPair) PublicJWKS() ([]byte, error) {
	return json.Marshal(map[string][]jwk{"keys": {k.publicJWK()}})
}

func (k KeyPair) Sign(c Claims) (string, error) {
	if c.TTL == 0 {
		return "", errors.New("ttl required")
	}
	issued := c.IssuedAt
	if issued.IsZero() {
		issued = time.Now()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": c.Issuer,
		"aud": c.Audience,
		"sub": c.Subject,
		"tid": c.Tenant,
		"iat": issued.Unix(),
		"exp": issued.Add(c.TTL).Unix(),
	})
	token.Header["kid"] = k.KeyID
	signed, err := token.SignedString(k.Private)
	if err != nil {
		return "", fmt.Errorf("sign dev token: %w", err)
	}
	return signed, nil
}

func (k KeyPair) publicJWK() jwk {
	return jwk{
		KeyType: "OKP",
		Curve:   "Ed25519",
		KeyID:   k.KeyID,
		Alg:     "EdDSA",
		Use:     "sig",
		X:       encode(k.Private.Public().(ed25519.PublicKey)),
	}
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decode(s string) ([]byte, error) { return base64.RawURLEncoding.Strict().DecodeString(s) }
