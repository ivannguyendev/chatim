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

type AuditEvent struct {
	TenantID string
	At       time.Time
	Actor    string
	Action   string
	UserID   string
	Detail   map[string]string
}

type Revocation struct {
	By     string
	Reason string
	At     time.Time
}

type TrustedIssuer struct {
	Issuer      string
	TenantID    string
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
	SetProfile(ctx context.Context, tenantID, userID string, update ProfileUpdate, audit AuditEvent) error
	CreateGroup(ctx context.Context, g Group, audit AuditEvent) error
	UpdateGroup(ctx context.Context, tenantID, id string, update GroupUpdate, audit AuditEvent) error
	DeleteGroup(ctx context.Context, tenantID, id string, audit AuditEvent) error
	SetUserGroups(ctx context.Context, tenantID, userID string, groupIDs []string, audit AuditEvent) error
	Group(ctx context.Context, tenantID, id string) (Group, error)
}

type Admin interface {
	Directory
	CreateTenant(ctx context.Context, t Tenant, audit AuditEvent) error
	SetTenantState(ctx context.Context, id string, state State, r Revocation, audit AuditEvent) error
	SetUserState(ctx context.Context, tenantID, id string, state State, audit AuditEvent) error
	CreateSigningKey(ctx context.Context, k SigningKey, audit AuditEvent) (time.Time, error)
	ActivateSigningKey(ctx context.Context, id string, lead time.Duration, audit AuditEvent) error
	SigningKeys(ctx context.Context) ([]SigningKey, error)
	CreateTrustedIssuer(ctx context.Context, issuer, tenantID string, audit AuditEvent) error
	SetTrustedIssuerState(ctx context.Context, issuer string, state State, audit AuditEvent) error
	AddTrustedKey(ctx context.Context, k TrustedKey, audit AuditEvent) error
	RemoveTrustedKey(ctx context.Context, issuer, keyID string, audit AuditEvent) error
	TrustedKeysOf(ctx context.Context, issuer string) ([]TrustedKey, error)
}

type ChangeKind int16

const (
	ChangeUser  ChangeKind = 1
	ChangeGroup ChangeKind = 2
)

type Position struct {
	Xid    uint64
	Kind   ChangeKind
	Tenant string
	ID     string
}

type UserChange struct {
	State       State
	DisplayName string
	AvatarURL   string
	Groups      []string
}

type GroupChange struct {
	Name        string
	ParentID    string
	Mentionable bool
	State       GroupState
}

type Change struct {
	Kind     ChangeKind
	TenantID string
	ID       string
	Ver      int64
	User     *UserChange
	Group    *GroupChange
}

type ChangePage struct {
	Changes             []Change
	Next                Position
	PendingAboveHorizon bool
}

type UserKey struct {
	TenantID string
	ID       string
}

type DirectoryEntry struct {
	TenantID    string
	ID          string
	DisplayName string
	AvatarURL   string
	State       State
	Ver         int64
}

type DirectoryReader interface {
	Changes(ctx context.Context, after Position, limit int) (ChangePage, error)
	LoadStart(ctx context.Context) (Position, error)
	LoadPage(ctx context.Context, after UserKey, limit int) ([]DirectoryEntry, error)
	TryLoadSlot(ctx context.Context, slots int) (release func(), ok bool, err error)
}

type Notice int8

const (
	NoticeNone Notice = iota
	NoticeDirectory
	NoticeRevocations
	NoticeOther
)

var ErrListenerBroken = errors.New("change listener broken")

type Listener interface {
	Wait(ctx context.Context) (Notice, error)
	Close(ctx context.Context) error
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
