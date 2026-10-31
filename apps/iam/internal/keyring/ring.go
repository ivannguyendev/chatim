package keyring

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

var (
	ErrNoActiveKey = errors.New("no active signing key")
	errBadToken    = fmt.Errorf("%w: invalid access token", apperr.ErrUnauthenticated)
)

type Source interface {
	SigningKeys(ctx context.Context) ([]store.SigningKey, error)
}

type Config struct {
	Token  signing.TokenConfig
	Leeway time.Duration
	Now    func() time.Time
}

type Ring struct {
	src      Source
	sealer   *signing.Sealer
	cfg      Config
	mu       sync.RWMutex
	signer   *signing.Signer
	verifier *jwtverify.Verifier
	jwks     []byte
}

func New(src Source, sealer *signing.Sealer, cfg Config) *Ring {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Ring{src: src, sealer: sealer, cfg: cfg}
}

func Published(keys []store.SigningKey, now time.Time, keep time.Duration) []signing.PublicKey {
	out := make([]signing.PublicKey, 0, len(keys))
	for _, k := range keys {
		if k.State == store.KeyRetired && now.Sub(k.RetiredAt) > keep {
			continue
		}
		out = append(out, signing.PublicKey{ID: k.ID, Key: ed25519.PublicKey(k.PublicKey)})
	}
	return out
}

func (r *Ring) Reload(ctx context.Context) error {
	keys, err := r.src.SigningKeys(ctx)
	if err != nil {
		return err
	}
	now := r.cfg.Now()
	set, err := signing.JWKS(Published(keys, now, r.cfg.Token.TTL+r.cfg.Leeway))
	if err != nil {
		return err
	}
	var signer *signing.Signer
	for _, k := range keys {
		if k.State != store.KeyActive {
			continue
		}
		opened, err := r.sealer.Open(k.ID, k.PrivateSealed)
		if err != nil {
			return fmt.Errorf("open signing key %s: %w", k.ID, err)
		}
		if signer, err = signing.NewSigner(opened, r.cfg.Token); err != nil {
			return err
		}
	}
	var verifier *jwtverify.Verifier
	if len(keys) > 0 {
		verifier, err = jwtverify.New(jwtverify.Config{JWKS: set, Issuer: r.cfg.Token.Issuer, Audience: r.cfg.Token.Audience, Leeway: r.cfg.Leeway, Now: r.cfg.Now})
		if err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.signer, r.verifier, r.jwks = signer, verifier, set
	r.mu.Unlock()
	if signer == nil {
		return ErrNoActiveKey
	}
	return nil
}

func (r *Ring) Ready() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.signer != nil
}

func (r *Ring) JWKS() ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.jwks, r.jwks != nil
}

func (r *Ring) Current() (*signing.Signer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.signer == nil {
		return nil, ErrNoActiveKey
	}
	return r.signer, nil
}

func (r *Ring) Claims(token string) (jwtverify.Claims, error) {
	r.mu.RLock()
	verifier := r.verifier
	r.mu.RUnlock()
	if verifier == nil {
		return jwtverify.Claims{}, errBadToken
	}
	claims, err := verifier.Verify(token)
	if err != nil || !session.ValidID(claims.Session) {
		return jwtverify.Claims{}, errBadToken
	}
	return claims, nil
}

func (r *Ring) Caller(token string) (auth.Caller, error) {
	claims, err := r.Claims(token)
	if err != nil {
		return auth.Caller{}, err
	}
	return auth.Caller{Tenant: claims.Tenant, Account: claims.User, Session: claims.Session}, nil
}
