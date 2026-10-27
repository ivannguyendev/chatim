package jwtverify

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

const maxSessionLength = 128

type Config struct {
	JWKS     []byte
	Issuer   string
	Audience string
	Leeway   time.Duration
	Now      func() time.Time
}

type Claims struct {
	Tenant    string
	User      string
	Session   string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type keyMap = map[string]ed25519.PublicKey

type Verifier struct {
	keys    keyMap
	dynamic atomic.Pointer[keyMap]
	parser  *jwt.Parser
}

type tokenClaims struct {
	jwt.RegisteredClaims
	Tenant  string `json:"tid"`
	Session string `json:"sid"`
}

func LoadFile(path string, cfg Config) (*Verifier, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key set: %w", err)
	}
	cfg.JWKS = data
	return New(cfg)
}

func New(cfg Config) (*Verifier, error) {
	switch {
	case cfg.Issuer == "":
		return nil, fmt.Errorf("%w: issuer required", ErrInvalidConfig)
	case cfg.Audience == "":
		return nil, fmt.Errorf("%w: audience required", ErrInvalidConfig)
	case cfg.Leeway < 0:
		return nil, fmt.Errorf("%w: leeway must not be negative", ErrInvalidConfig)
	}
	keys, err := parseKeySet(cfg.JWKS, false)
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(cfg.Leeway),
		jwt.WithTimeFunc(now),
		jwt.WithStrictDecoding(),
	)
	return &Verifier{keys: keys, parser: parser}, nil
}

func (v *Verifier) SetDynamic(jwks []byte) (int, error) {
	keys, err := parseKeySet(jwks, true)
	if err != nil {
		return 0, err
	}
	conflicts := 0
	for kid, key := range keys {
		fixed, ok := v.keys[kid]
		if !ok {
			continue
		}
		if !fixed.Equal(key) {
			conflicts++
		}
		delete(keys, kid)
	}
	v.dynamic.Store(&keys)
	return conflicts, nil
}

func (v *Verifier) Verify(token string) (Claims, error) {
	var claims tokenClaims
	parsed, err := v.parser.ParseWithClaims(token, &claims, v.key)
	if err != nil {
		return Claims{}, invalid(classify(parsed, err))
	}
	switch {
	case ids.ValidUser(claims.Subject) != nil:
		return Claims{}, invalid(ReasonSubject)
	case ids.ValidTenant(claims.Tenant) != nil:
		return Claims{}, invalid(ReasonTenant)
	case claims.Session == "" || claims.IssuedAt == nil:
		return Claims{}, invalid(ReasonMissingClaim)
	case !validSession(claims.Session):
		return Claims{}, invalid(ReasonSession)
	}
	return Claims{
		Tenant:    claims.Tenant,
		User:      claims.Subject,
		Session:   claims.Session,
		IssuedAt:  claims.IssuedAt.Time,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

func KeyIDOf(token string) string {
	header, _, ok := strings.Cut(token, ".")
	if !ok {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(header)
	if err != nil {
		return ""
	}
	var h struct {
		KeyID string `json:"kid"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return ""
	}
	return h.KeyID
}

func validSession(s string) bool {
	if len(s) > maxSessionLength {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func (v *Verifier) key(token *jwt.Token) (any, error) {
	if typ, present := token.Header["typ"]; present {
		if s, ok := typ.(string); !ok || !strings.EqualFold(s, "JWT") {
			return nil, invalid(ReasonType)
		}
	}
	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, invalid(ReasonKeyID)
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	if dynamic := v.dynamic.Load(); dynamic != nil {
		if key, ok := (*dynamic)[kid]; ok {
			return key, nil
		}
	}
	return nil, invalid(ReasonKeyID)
}

func classify(token *jwt.Token, err error) Reason {
	if reason := ReasonOf(err); reason != "" {
		return reason
	}
	switch {
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ReasonMalformed
	case errors.Is(err, jwt.ErrTokenUnverifiable):
		return ReasonAlgorithm
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		if token == nil || token.Method == nil || token.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
			return ReasonAlgorithm
		}
		return ReasonSignature
	case errors.Is(err, jwt.ErrTokenExpired):
		return ReasonExpired
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return ReasonNotYetValid
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return ReasonIssuer
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return ReasonAudience
	case errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return ReasonMissingClaim
	default:
		return ReasonClaims
	}
}
