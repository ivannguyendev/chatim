package signing

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type TokenConfig struct {
	Issuer   string
	Audience string
	TTL      time.Duration
}

type Claims struct {
	Tenant   string
	Subject  string
	Session  string
	IssuedAt time.Time
}

type Signer struct {
	key Key
	cfg TokenConfig
}

type accessClaims struct {
	jwt.RegisteredClaims
	Tenant  string `json:"tid"`
	Session string `json:"sid"`
}

func NewSigner(key Key, cfg TokenConfig) (*Signer, error) {
	switch {
	case cfg.Issuer == "":
		return nil, errors.New("signer: issuer required")
	case cfg.Audience == "":
		return nil, errors.New("signer: audience required")
	case cfg.TTL <= 0:
		return nil, errors.New("signer: ttl must be positive")
	case len(key.Private) == 0 || key.ID == "":
		return nil, errors.New("signer: key required")
	}
	return &Signer{key: key, cfg: cfg}, nil
}

func (s *Signer) KeyID() string { return s.key.ID }

func (s *Signer) Sign(c Claims) (string, time.Time, error) {
	issued := c.IssuedAt.Truncate(time.Second)
	expires := issued.Add(s.cfg.TTL)
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Audience:  jwt.ClaimStrings{s.cfg.Audience},
			Subject:   c.Subject,
			IssuedAt:  jwt.NewNumericDate(issued),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
		Tenant:  c.Tenant,
		Session: c.Session,
	})
	token.Header["kid"] = s.key.ID
	signed, err := token.SignedString(s.key.Private)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expires, nil
}
