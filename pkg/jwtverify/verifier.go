package jwtverify

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

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
	ExpiresAt time.Time
}

type Verifier struct {
	keys   map[string]ed25519.PublicKey
	parser *jwt.Parser
}

type tokenClaims struct {
	jwt.RegisteredClaims
	Tenant string `json:"tid"`
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
	keys, err := parseKeySet(cfg.JWKS)
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

func (v *Verifier) Verify(token string) (Claims, error) {
	var claims tokenClaims
	parsed, err := v.parser.ParseWithClaims(token, &claims, v.key)
	if err != nil {
		return Claims{}, invalid(classify(parsed, err))
	}
	if ids.ValidUser(claims.Subject) != nil {
		return Claims{}, invalid(ReasonSubject)
	}
	if ids.ValidTenant(claims.Tenant) != nil {
		return Claims{}, invalid(ReasonTenant)
	}
	return Claims{Tenant: claims.Tenant, User: claims.Subject, ExpiresAt: claims.ExpiresAt.Time}, nil
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
	key, ok := v.keys[kid]
	if !ok {
		return nil, invalid(ReasonKeyID)
	}
	return key, nil
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
