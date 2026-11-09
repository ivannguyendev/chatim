package store

import (
	"context"
	"errors"
	"time"
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

type GroupState int16

const (
	GroupLive    GroupState = 1
	GroupDeleted GroupState = 2
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

type User struct {
	TenantID    string
	ID          string
	DisplayName string
	AvatarURL   string
	State       State
	Ver         int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	UpdatedBy   string
}

type Group struct {
	TenantID    string
	ID          string
	Name        string
	ParentID    string
	Mentionable bool
	State       GroupState
	CreatedAt   time.Time
	UpdatedAt   time.Time
	UpdatedBy   string
}

type GroupUpdate struct {
	Name        *string
	ParentID    *string
	Mentionable *bool
}

type ProfileUpdate struct {
	DisplayName *string
	AvatarURL   *string
}

type Profile struct {
	ID          string
	DisplayName string
	AvatarURL   string
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

type Revocation struct {
	By     string
	Reason string
	At     time.Time
}

type TrustedIssuer struct {
	Issuer      string
	TenantID    string
	Realm       string
	IssuerState State
	TenantState State
}

type TrustedKey struct {
	Issuer    string
	KeyID     string
	Algorithm string
	PublicKey []byte
	State     State
	CreatedAt time.Time
	CreatedBy string
}

type SeenProfile struct {
	DisplayName string
	AvatarURL   string
}

type ProfileWrite struct {
	TenantID    string
	UserID      string
	DisplayName string
	AvatarURL   string
	Seen        *SeenProfile
	At          time.Time
	By          string
}

type SchemaState struct {
	Missing  []string
	Outdated []string
}

type TrustSource interface {
	TrustedIssuers(ctx context.Context) ([]TrustedIssuer, error)
	TrustedKeys(ctx context.Context) ([]TrustedKey, error)
}

type ProfileWriter interface {
	WriteProfile(ctx context.Context, w ProfileWrite) (written bool, err error)
}

type Directory interface {
	SetProfile(ctx context.Context, tenantID, userID string, update ProfileUpdate, at time.Time, by string) error
	CreateGroup(ctx context.Context, g Group) error
	UpdateGroup(ctx context.Context, tenantID, id string, update GroupUpdate, at time.Time, by string) error
	DeleteGroup(ctx context.Context, tenantID, id string, at time.Time, by string) (int, error)
	SetUserGroups(ctx context.Context, tenantID, userID string, groupIDs []string, at time.Time, by string) error
	Group(ctx context.Context, tenantID, id string) (Group, error)
}

type Admin interface {
	Directory
	CreateTenant(ctx context.Context, t Tenant) error
	SetTenantState(ctx context.Context, id string, state State, r Revocation) error
	SetUserState(ctx context.Context, tenantID, id string, state State, at time.Time, by string) error
	CreateSigningKey(ctx context.Context, k SigningKey) (time.Time, error)
	ActivateSigningKey(ctx context.Context, id string, lead time.Duration) error
	SigningKeys(ctx context.Context) ([]SigningKey, error)
	CreateTrustedIssuer(ctx context.Context, issuer, tenantID, realm string, at time.Time, by string) error
	SetTrustedIssuerState(ctx context.Context, issuer string, state State, at time.Time, by string) error
	AddTrustedKey(ctx context.Context, k TrustedKey, at time.Time, by string) error
	RemoveTrustedKey(ctx context.Context, issuer, keyID string) error
	TrustedKeysOf(ctx context.Context, issuer string) ([]TrustedKey, error)
}

type UserKey struct {
	TenantID string
	ID       string
}

type SubjectKey struct {
	TenantID string
	Realm    string
	Sub      string
}

type DirectoryEntry struct {
	TenantID    string
	ID          string
	DisplayName string
	AvatarURL   string
	State       State
	Ver         int64
	Realm       string
	Sub         string
}

type StreamToken []byte

type EventKind int8

const (
	EventUser EventKind = iota + 1
	EventTrust
	EventRevocation
)

type StreamEvent struct {
	Kinds    []EventKind
	User     DirectoryEntry
	Token    StreamToken
	CaughtUp bool
}

var ErrStreamLost = errors.New("change stream lost")

type ChangeStream interface {
	Next(ctx context.Context) (StreamEvent, bool, error)
	Close(ctx context.Context) error
}

type Slot interface {
	Renew(ctx context.Context) (bool, error)
	Release()
}
