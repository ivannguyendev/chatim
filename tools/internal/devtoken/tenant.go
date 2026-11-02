package devtoken

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AlgorithmEdDSA = "EdDSA"
	AlgorithmES256 = "ES256"
	AlgorithmRS256 = "RS256"

	tenantRSABits   = 2048
	p256CoordLength = 32
)

type TenantKey struct {
	KeyID     string
	Algorithm string
	Private   crypto.Signer
}

type TenantClaims struct {
	Issuer   string
	Audience string
	Subject  string
	Tenant   string
	Name     string
	Picture  string
	IssuedAt time.Time
	TTL      time.Duration
	KeyID    string
}

type tenantJWK struct {
	KeyType  string `json:"kty"`
	Curve    string `json:"crv,omitempty"`
	KeyID    string `json:"kid"`
	Alg      string `json:"alg"`
	X        string `json:"x,omitempty"`
	Y        string `json:"y,omitempty"`
	Modulus  string `json:"n,omitempty"`
	Exponent string `json:"e,omitempty"`
	D        string `json:"d,omitempty"`
	P        string `json:"p,omitempty"`
	Q        string `json:"q,omitempty"`
	DP       string `json:"dp,omitempty"`
	DQ       string `json:"dq,omitempty"`
	QI       string `json:"qi,omitempty"`
}

func GenerateTenantKey(alg, kid string) (TenantKey, error) {
	if kid == "" {
		return TenantKey{}, fmt.Errorf("%w: kid required", ErrInvalidKey)
	}
	var (
		private crypto.Signer
		err     error
	)
	switch alg {
	case AlgorithmEdDSA:
		_, private, err = ed25519.GenerateKey(rand.Reader)
	case AlgorithmES256:
		private, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case AlgorithmRS256:
		private, err = rsa.GenerateKey(rand.Reader, tenantRSABits)
	default:
		return TenantKey{}, fmt.Errorf("%w: algorithm %q, want EdDSA, ES256 or RS256", ErrInvalidKey, alg)
	}
	if err != nil {
		return TenantKey{}, fmt.Errorf("generate %s key: %w", alg, err)
	}
	return TenantKey{KeyID: kid, Algorithm: alg, Private: private}, nil
}

func ParseTenantKey(data []byte) (TenantKey, error) {
	var key tenantJWK
	if err := json.Unmarshal(data, &key); err != nil {
		return TenantKey{}, fmt.Errorf("%w: not a JWK", ErrInvalidKey)
	}
	if key.KeyID == "" {
		return TenantKey{}, fmt.Errorf("%w: kid required", ErrInvalidKey)
	}
	var (
		private crypto.Signer
		err     error
	)
	switch {
	case key.Alg == AlgorithmEdDSA && key.KeyType == "OKP":
		var pair KeyPair
		pair, err = ParsePrivateJWK(data)
		private = pair.Private
	case key.Alg == AlgorithmES256 && key.KeyType == "EC" && key.Curve == "P-256":
		private, err = parseECPrivate(key)
	case key.Alg == AlgorithmRS256 && key.KeyType == "RSA":
		private, err = parseRSAPrivate(key)
	default:
		return TenantKey{}, fmt.Errorf("%w: alg %q does not match kty %q crv %q", ErrInvalidKey, key.Alg, key.KeyType, key.Curve)
	}
	if err != nil {
		return TenantKey{}, err
	}
	out := TenantKey{KeyID: key.KeyID, Algorithm: key.Alg, Private: private}
	derived, err := out.publicJWK()
	if err != nil {
		return TenantKey{}, err
	}
	if (key.X != "" && key.X != derived.X) || (key.Y != "" && key.Y != derived.Y) {
		return TenantKey{}, fmt.Errorf("%w: public point does not match d", ErrInvalidKey)
	}
	return out, nil
}

func (k TenantKey) PublicJWK() ([]byte, error) {
	key, err := k.publicJWK()
	if err != nil {
		return nil, err
	}
	return json.Marshal(key)
}

func (k TenantKey) PrivateJWK() ([]byte, error) {
	key, err := k.publicJWK()
	if err != nil {
		return nil, err
	}
	switch private := k.Private.(type) {
	case ed25519.PrivateKey:
		key.D = encode(private.Seed())
	case *ecdsa.PrivateKey:
		raw, err := private.Bytes()
		if err != nil {
			return nil, fmt.Errorf("encode ES256 key: %w", err)
		}
		key.D = encode(raw)
	case *rsa.PrivateKey:
		private.Precompute()
		key.D = encode(private.D.Bytes())
		key.P = encode(private.Primes[0].Bytes())
		key.Q = encode(private.Primes[1].Bytes())
		key.DP = encode(private.Precomputed.Dp.Bytes())
		key.DQ = encode(private.Precomputed.Dq.Bytes())
		key.QI = encode(private.Precomputed.Qinv.Bytes())
	}
	return json.Marshal(key)
}

func (k TenantKey) Sign(c TenantClaims) (string, error) {
	if c.TTL == 0 {
		return "", errors.New("ttl required")
	}
	method := jwt.GetSigningMethod(k.Algorithm)
	if method == nil {
		return "", fmt.Errorf("%w: algorithm %q", ErrInvalidKey, k.Algorithm)
	}
	issued := c.IssuedAt
	if issued.IsZero() {
		issued = time.Now()
	}
	claims := jwt.MapClaims{
		"iss": c.Issuer,
		"aud": c.Audience,
		"sub": c.Subject,
		"iat": issued.Unix(),
		"exp": issued.Add(c.TTL).Unix(),
	}
	for name, value := range map[string]string{"tid": c.Tenant, "name": c.Name, "picture": c.Picture} {
		if value != "" {
			claims[name] = value
		}
	}
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = k.KeyID
	if c.KeyID != "" {
		token.Header["kid"] = c.KeyID
	}
	signed, err := token.SignedString(k.Private)
	if err != nil {
		return "", fmt.Errorf("sign tenant token: %w", err)
	}
	return signed, nil
}

func (k TenantKey) publicJWK() (tenantJWK, error) {
	key := tenantJWK{KeyID: k.KeyID, Alg: k.Algorithm}
	switch public := k.Private.Public().(type) {
	case ed25519.PublicKey:
		key.KeyType, key.Curve, key.X = "OKP", "Ed25519", encode(public)
	case *ecdsa.PublicKey:
		point, err := public.Bytes()
		if err != nil || len(point) != 1+2*p256CoordLength {
			return tenantJWK{}, fmt.Errorf("%w: want a P-256 key", ErrInvalidKey)
		}
		key.KeyType, key.Curve = "EC", "P-256"
		key.X, key.Y = encode(point[1:1+p256CoordLength]), encode(point[1+p256CoordLength:])
	case *rsa.PublicKey:
		key.KeyType = "RSA"
		key.Modulus, key.Exponent = encode(public.N.Bytes()), encode(big.NewInt(int64(public.E)).Bytes())
	default:
		return tenantJWK{}, fmt.Errorf("%w: unsupported key type %T", ErrInvalidKey, public)
	}
	return key, nil
}

func parseECPrivate(key tenantJWK) (crypto.Signer, error) {
	d, err := decode(key.D)
	if err != nil {
		return nil, fmt.Errorf("%w: d must be base64url", ErrInvalidKey)
	}
	private, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		return nil, fmt.Errorf("%w: d is not a P-256 scalar", ErrInvalidKey)
	}
	return private, nil
}

func parseRSAPrivate(key tenantJWK) (crypto.Signer, error) {
	parts := make([]*big.Int, 0, 5)
	for _, field := range []string{key.Modulus, key.Exponent, key.D, key.P, key.Q} {
		b, err := decode(field)
		if err != nil || len(b) == 0 {
			return nil, fmt.Errorf("%w: n, e, d, p and q must be base64url", ErrInvalidKey)
		}
		parts = append(parts, new(big.Int).SetBytes(b))
	}
	if !parts[1].IsInt64() {
		return nil, fmt.Errorf("%w: e too large", ErrInvalidKey)
	}
	private := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: parts[0], E: int(parts[1].Int64())},
		D:         parts[2],
		Primes:    []*big.Int{parts[3], parts[4]},
	}
	if err := private.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}
	private.Precompute()
	return private, nil
}
