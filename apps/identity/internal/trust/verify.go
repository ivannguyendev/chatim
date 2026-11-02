package trust

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

type Reason string

const (
	ReasonHeader         Reason = "header"
	ReasonIssuer         Reason = "issuer"
	ReasonKey            Reason = "key"
	ReasonAlgorithm      Reason = "algorithm"
	ReasonSignature      Reason = "signature"
	ReasonAudience       Reason = "audience"
	ReasonTime           Reason = "time"
	ReasonLifetime       Reason = "lifetime"
	ReasonTenantDisabled Reason = "tenant_disabled"
	ReasonTenantMismatch Reason = "tenant_mismatch"
	ReasonSubject        Reason = "subject"
)

var (
	ErrRefused       = errors.New("tenant token refused")
	ErrNotReady      = fmt.Errorf("trusted issuers not loaded: %w", apperr.ErrUnavailable)
	ErrInvalidConfig = errors.New("invalid trust config")
)

var allowedAlgorithms = []string{AlgorithmEdDSA, AlgorithmES256, AlgorithmRS256}

type refusal struct {
	reason Reason
}

func (refusal) Error() string { return ErrRefused.Error() }

func (refusal) Unwrap() error { return ErrRefused }

func refuse(reason Reason) error { return refusal{reason: reason} }

func ReasonOf(err error) Reason {
	if r, ok := errors.AsType[refusal](err); ok {
		return r.reason
	}
	return ""
}

type Config struct {
	Audience    string
	MaxLifetime time.Duration
	Leeway      time.Duration
	Now         func() time.Time
}

type Subject struct {
	Tenant     string
	User       string
	Name       string
	HasName    bool
	Picture    string
	HasPicture bool
	Issuer     string
	KeyID      string
}

type Verifier struct {
	cfg    Config
	parser *jwt.Parser
	snap   atomic.Pointer[snapshot]
	stats  Stats
}

type match struct {
	issuer string
	keyID  string
	entry  issuerEntry
}

func New(cfg Config) (*Verifier, error) {
	switch {
	case cfg.Audience == "":
		return nil, fmt.Errorf("%w: audience required", ErrInvalidConfig)
	case cfg.MaxLifetime <= 0:
		return nil, fmt.Errorf("%w: max lifetime must be positive", ErrInvalidConfig)
	case cfg.Leeway < 0:
		return nil, fmt.Errorf("%w: leeway must not be negative", ErrInvalidConfig)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods(allowedAlgorithms),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(cfg.Leeway),
		jwt.WithTimeFunc(cfg.Now),
		jwt.WithJSONNumber(),
		jwt.WithStrictDecoding(),
	)
	return &Verifier{cfg: cfg, parser: parser}, nil
}

func (v *Verifier) Stats() *Stats { return &v.stats }

func (v *Verifier) Verify(token string) (Subject, error) {
	snap := v.snap.Load()
	if snap == nil {
		return Subject{}, ErrNotReady
	}
	claims := jwt.MapClaims{}
	var found match
	parsed, err := v.parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return snap.key(t, claims, &found)
	})
	if err != nil {
		return Subject{}, refuse(v.classify(parsed, claims, err))
	}
	return v.subject(claims, found)
}

func (s *snapshot) key(t *jwt.Token, claims jwt.MapClaims, found *match) (any, error) {
	if _, crit := t.Header["crit"]; crit {
		return nil, refuse(ReasonHeader)
	}
	kid, ok := t.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, refuse(ReasonHeader)
	}
	iss, ok := claims["iss"].(string)
	entry, known := s.issuers[iss]
	if !ok || !known {
		return nil, refuse(ReasonIssuer)
	}
	key, ok := entry.keys[kid]
	if !ok {
		return nil, refuse(ReasonKey)
	}
	if t.Method.Alg() != key.algorithm {
		return nil, refuse(ReasonAlgorithm)
	}
	*found = match{issuer: iss, keyID: kid, entry: entry}
	return key.public, nil
}

func (v *Verifier) classify(parsed *jwt.Token, claims jwt.MapClaims, err error) Reason {
	if reason := ReasonOf(err); reason != "" {
		return reason
	}
	switch {
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ReasonHeader
	case errors.Is(err, jwt.ErrTokenUnverifiable):
		return ReasonAlgorithm
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		if parsed == nil || parsed.Method == nil || !slices.Contains(allowedAlgorithms, parsed.Method.Alg()) {
			return ReasonAlgorithm
		}
		return ReasonSignature
	case !v.audienceMatches(claims):
		return ReasonAudience
	case errors.Is(err, jwt.ErrTokenInvalidClaims):
		return ReasonTime
	default:
		return ReasonHeader
	}
}

func (v *Verifier) audienceMatches(claims jwt.MapClaims) bool {
	aud, err := claims.GetAudience()
	return err == nil && slices.Contains(aud, v.cfg.Audience)
}

func (v *Verifier) subject(claims jwt.MapClaims, found match) (Subject, error) {
	iat, errIat := claims.GetIssuedAt()
	exp, errExp := claims.GetExpirationTime()
	if errIat != nil || errExp != nil || iat == nil || exp == nil {
		return Subject{}, refuse(ReasonTime)
	}
	if lifetime := exp.Sub(iat.Time); lifetime <= 0 || lifetime > v.cfg.MaxLifetime {
		return Subject{}, refuse(ReasonLifetime)
	}
	if !found.entry.tenantActive {
		return Subject{}, refuse(ReasonTenantDisabled)
	}
	if raw, present := claims["tid"]; present {
		if tid, ok := raw.(string); !ok || tid != found.entry.tenant {
			return Subject{}, refuse(ReasonTenantMismatch)
		}
	}
	sub, ok := claims["sub"].(string)
	if !ok || ids.ValidUser(sub) != nil {
		return Subject{}, refuse(ReasonSubject)
	}
	name, hasName := claims["name"].(string)
	picture, hasPicture := claims["picture"].(string)
	return Subject{
		Tenant:     found.entry.tenant,
		User:       sub,
		Name:       name,
		HasName:    hasName,
		Picture:    picture,
		HasPicture: hasPicture,
		Issuer:     found.issuer,
		KeyID:      found.keyID,
	}, nil
}
