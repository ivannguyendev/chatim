package auth

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const maxUsernameLength = 64

var (
	ErrInvalidCredentials     = fmt.Errorf("%w: invalid credentials", apperr.ErrUnauthenticated)
	ErrPasswordChangeRequired = fmt.Errorf("%w: password change required", apperr.ErrFailedPrecondition)
	ErrInvalidGrant           = fmt.Errorf("%w: invalid grant", apperr.ErrUnauthenticated)
	ErrProofReplayed          = fmt.Errorf("%w: dpop proof replayed", apperr.ErrInvalidArgument)
	ErrUnsupportedTokenType   = fmt.Errorf("%w: unsupported token type", apperr.ErrInvalidArgument)
	ErrSessionInvalid         = fmt.Errorf("%w: session is not active", apperr.ErrUnauthenticated)
	errTooManyRequests        = fmt.Errorf("%w: too many login attempts", apperr.ErrResourceExhausted)
	errBusy                   = fmt.Errorf("%w: login busy", apperr.ErrUnavailable)
)

type Hasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
	VerifyDummy(password string)
	NeedsRehash(encoded string) bool
}

type Signers interface {
	Current() (*signing.Signer, error)
}

type Deps struct {
	Store   store.Auth
	Hasher  Hasher
	Policy  password.Policy
	Backoff guard.Backoff
	IPs     *guard.IPLimiter
	Slots   *guard.HashSlots
	Signers Signers
	Random  io.Reader
	Now     func() time.Time
	Stats   *Stats
}

type Config struct {
	SessionIdle  time.Duration
	SessionMax   time.Duration
	RefreshGrace time.Duration
}

type Service struct {
	deps Deps
	cfg  Config
}

type Proof struct {
	Thumbprint string
	JTI        string
}

type Tokens struct {
	AccessToken  string
	ExpiresAt    time.Time
	RefreshToken string
	SessionID    string
}

type Caller struct {
	Tenant  string
	Account string
	Session string
}

func New(deps Deps, cfg Config) *Service {
	if deps.Stats == nil {
		deps.Stats = &Stats{}
	}
	return &Service{deps: deps, cfg: cfg}
}

func (s *Service) Stats() *Stats { return s.deps.Stats }

func NormalizeUsername(raw string) (string, error) {
	u := strings.ToLower(raw)
	if u == "" || len(u) > maxUsernameLength {
		return "", fmt.Errorf("%w: username", apperr.ErrInvalidArgument)
	}
	for i := range len(u) {
		c := u[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.IndexByte("._@+-", c) >= 0
		if !ok {
			return "", fmt.Errorf("%w: username", apperr.ErrInvalidArgument)
		}
	}
	return u, nil
}

func validIdentity(tenant, username string) (string, bool) {
	if ids.ValidTenant(tenant) != nil {
		return "", false
	}
	u, err := NormalizeUsername(username)
	return u, err == nil
}

func (s *Service) signAccess(tenant, account, sid string, now time.Time) (string, time.Time, error) {
	signer, err := s.deps.Signers.Current()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: no signing key", apperr.ErrUnavailable)
	}
	return signer.Sign(signing.Claims{Tenant: tenant, Subject: account, Session: sid, IssuedAt: now})
}

func isNotFound(err error) bool { return errors.Is(err, apperr.ErrNotFound) }
