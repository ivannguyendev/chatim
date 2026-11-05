package exchange

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	maxLoggedClaim     = 255
	reasonUserDisabled = "user_disabled"
)

var (
	ErrRefused  = fmt.Errorf("%w: tenant token refused", apperr.ErrUnauthenticated)
	ErrDisabled = fmt.Errorf("%w: user or tenant disabled", apperr.ErrPermissionDenied)
)

type Verifier interface {
	Verify(token string) (trust.Subject, error)
}

type Directory interface {
	Ready() bool
	LookupOne(tenant, id string) (directory.User, bool)
}

type Signers interface {
	Current() (*signing.Signer, error)
}

type Deps struct {
	Verifier  Verifier
	Directory Directory
	Profiles  store.ProfileWriter
	Signers   Signers
	Random    io.Reader
	Now       func() time.Time
	Stats     *Stats
	Log       *slog.Logger
}

type Issued struct {
	AccessToken string
	ExpiresAt   time.Time
}

type Service struct {
	deps Deps
}

func New(deps Deps) *Service {
	if deps.Stats == nil {
		deps.Stats = &Stats{}
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Random == nil {
		deps.Random = rand.Reader
	}
	return &Service{deps: deps}
}

func (s *Service) Stats() *Stats { return s.deps.Stats }

func (s *Service) Exchange(ctx context.Context, subjectToken, clientIP string) (Issued, error) {
	issued, err := s.exchange(ctx, subjectToken, clientIP)
	if err != nil && !errors.Is(err, ErrRefused) && !errors.Is(err, ErrDisabled) && !errors.Is(err, context.Canceled) {
		s.deps.Log.WarnContext(ctx, "token exchange unavailable", slog.String("error", err.Error()), slog.String("ip", clientIP))
	}
	return issued, err
}

func (s *Service) exchange(ctx context.Context, subjectToken, clientIP string) (Issued, error) {
	if !s.deps.Directory.Ready() {
		return Issued{}, fmt.Errorf("%w: directory not loaded", apperr.ErrUnavailable)
	}
	sub, err := s.deps.Verifier.Verify(subjectToken)
	if err != nil {
		return Issued{}, s.refused(ctx, subjectToken, clientIP, err)
	}
	cached, known := s.deps.Directory.LookupOne(sub.Tenant, sub.User)
	if known && cached.State != store.StateActive {
		s.deps.Log.InfoContext(ctx, "token exchange refused", slog.String("reason", reasonUserDisabled),
			slog.String("tenant", sub.Tenant), slog.String("user", sub.User), slog.String("iss", sub.Issuer),
			slog.String("kid", sub.KeyID), slog.String("ip", clientIP))
		return Issued{}, ErrDisabled
	}
	signer, err := s.deps.Signers.Current()
	if err != nil {
		return Issued{}, fmt.Errorf("%w: no signing key: %w", apperr.ErrUnavailable, err)
	}
	now := s.deps.Now()
	if err := s.writeProfile(ctx, sub, cached, known, now); err != nil {
		return Issued{}, err
	}
	return s.sign(signer, sub, now)
}

func (s *Service) refused(ctx context.Context, subjectToken, clientIP string, err error) error {
	if !errors.Is(err, trust.ErrRefused) {
		return fmt.Errorf("%w: verify tenant token: %w", apperr.ErrUnavailable, err)
	}
	reason := trust.ReasonOf(err)
	iss, kid := loggedClaims(subjectToken)
	s.deps.Log.InfoContext(ctx, "token exchange refused", slog.String("reason", string(reason)),
		slog.String("iss", iss), slog.String("kid", kid), slog.String("ip", clientIP))
	if reason == trust.ReasonTenantDisabled {
		return ErrDisabled
	}
	return ErrRefused
}

func (s *Service) writeProfile(ctx context.Context, sub trust.Subject, cached directory.User, known bool, now time.Time) error {
	w, needed := profileWrite(sub, cached, known, now)
	if !needed {
		return nil
	}
	written, err := s.deps.Profiles.WriteProfile(ctx, w)
	switch {
	case err != nil && ctx.Err() != nil:
		return fmt.Errorf("%w: write just-in-time profile: %w", apperr.ErrUnavailable, ctx.Err())
	case err != nil:
		s.deps.Stats.storeErrors.Add(1)
		s.deps.Stats.countWrite(WriteError)
		return fmt.Errorf("%w: write just-in-time profile: %w", apperr.ErrUnavailable, err)
	case !written:
		s.deps.Stats.countWrite(WriteConflict)
	case w.Seen == nil:
		s.deps.Stats.countWrite(WriteCreated)
	default:
		s.deps.Stats.countWrite(WriteUpdated)
	}
	return nil
}

func (s *Service) sign(signer *signing.Signer, sub trust.Subject, now time.Time) (Issued, error) {
	sid, err := signing.NewSessionID(s.deps.Random)
	if err != nil {
		return Issued{}, fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	token, expires, err := signer.Sign(signing.Claims{Tenant: sub.Tenant, Subject: sub.User, Session: sid, IssuedAt: now})
	if err != nil {
		return Issued{}, fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	return Issued{AccessToken: token, ExpiresAt: expires}, nil
}

func loggedClaims(token string) (iss, kid string) {
	claims := jwt.MapClaims{}
	parsed, _, _ := jwt.NewParser().ParseUnverified(token, claims)
	iss, _ = claims["iss"].(string)
	if parsed != nil {
		kid, _ = parsed.Header["kid"].(string)
	}
	return clip(iss), clip(kid)
}

func clip(s string) string {
	if len(s) > maxLoggedClaim {
		return s[:maxLoggedClaim]
	}
	return s
}
