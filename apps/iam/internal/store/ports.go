package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
)

type State int16

const (
	StateActive   State = 1
	StateDisabled State = 2
)

type KeyState int16

const (
	KeyPending KeyState = 1
	KeyActive  KeyState = 2
	KeyRetired KeyState = 3
)

type Tenant struct {
	ID        string
	Name      string
	State     State
	Ver       int64
	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy string
}

type Account struct {
	TenantID           string
	ID                 string
	Username           string
	Email              string
	State              State
	PasswordHash       string
	PasswordMustChange bool
	PasswordChangedAt  time.Time
	FailedLoginCount   int
	NextLoginAt        time.Time
	Ver                int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	UpdatedBy          string
}

type Session struct {
	ID            string
	TenantID      string
	AccountID     string
	State         State
	DeviceName    string
	UserAgent     string
	CreatedAt     time.Time
	LastUsedAt    time.Time
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
	RefreshVer    int64
	RefreshHash   []byte
	History       []session.HistoryEntry
	KeyThumbprint string
	RecentJTI     []string
	RevokedAt     time.Time
	RevokedBy     string
	RevokedReason string
}

type SigningKey struct {
	ID            string
	State         KeyState
	PublicKey     []byte
	PrivateSealed []byte
	CreatedAt     time.Time
	ActivatedAt   time.Time
	RetiredAt     time.Time
}

type AuditEvent struct {
	TenantID  string
	At        time.Time
	Actor     string
	Action    string
	AccountID string
	SessionID string
	IP        string
	Detail    map[string]string
}

type NewSession struct {
	Session    Session
	AccountVer int64
	Rehash     string
}

type Rotation struct {
	SessionID     string
	ExpectVer     int64
	RefreshHash   []byte
	History       []session.HistoryEntry
	RecentJTI     []string
	IdleExpiresAt time.Time
	LastUsedAt    time.Time
}

type Revocation struct {
	By     string
	Reason string
	At     time.Time
}

type PasswordChange struct {
	TenantID   string
	AccountID  string
	ExpectVer  int64
	Hash       string
	MustChange bool
	Revocation Revocation
}

type Auth interface {
	TenantByID(ctx context.Context, id string) (Tenant, error)
	AccountByUsername(ctx context.Context, tenantID, username string) (Account, error)
	AccountByID(ctx context.Context, tenantID, id string) (Account, error)
	CreateSession(ctx context.Context, s NewSession, audit AuditEvent) (time.Time, error)
	RecordLoginFailure(ctx context.Context, tenantID, accountID string, nextLoginAt func(failed int) time.Time, audit AuditEvent) error
	SessionByID(ctx context.Context, id string) (Session, error)
	RotateRefresh(ctx context.Context, r Rotation, audit AuditEvent) (time.Time, error)
	RevokeSession(ctx context.Context, id string, r Revocation, audit AuditEvent) (bool, error)
	ActiveSessions(ctx context.Context, tenantID, accountID string) ([]Session, error)
	SetPassword(ctx context.Context, c PasswordChange, audit AuditEvent) error
}

type Admin interface {
	Auth
	CreateTenant(ctx context.Context, t Tenant, audit AuditEvent) error
	SetTenantState(ctx context.Context, id string, state State, r Revocation, audit AuditEvent) error
	CreateAccount(ctx context.Context, a Account, audit AuditEvent) error
	SetAccountState(ctx context.Context, tenantID, id string, state State, r Revocation, audit AuditEvent) error
	CreateSigningKey(ctx context.Context, k SigningKey, audit AuditEvent) error
	ActivateSigningKey(ctx context.Context, id string, at time.Time, audit AuditEvent) error
	SigningKeys(ctx context.Context) ([]SigningKey, error)
}
