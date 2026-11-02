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

type Account struct {
	TenantID    string
	ID          string
	DisplayName string
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
	TenantID  string
	At        time.Time
	Actor     string
	Action    string
	AccountID string
	SessionID string
	IP        string
	Detail    map[string]string
}

type Revocation struct {
	By     string
	Reason string
	At     time.Time
}

type Directory interface {
	SetProfile(ctx context.Context, tenantID, accountID string, update ProfileUpdate, audit AuditEvent) error
	CreateGroup(ctx context.Context, g Group, audit AuditEvent) error
	UpdateGroup(ctx context.Context, tenantID, id string, update GroupUpdate, audit AuditEvent) error
	DeleteGroup(ctx context.Context, tenantID, id string, audit AuditEvent) error
	SetAccountGroups(ctx context.Context, tenantID, accountID string, groupIDs []string, audit AuditEvent) error
	Group(ctx context.Context, tenantID, id string) (Group, error)
}

type Admin interface {
	Directory
	CreateTenant(ctx context.Context, t Tenant, audit AuditEvent) error
	SetTenantState(ctx context.Context, id string, state State, r Revocation, audit AuditEvent) error
	SetAccountState(ctx context.Context, tenantID, id string, state State, audit AuditEvent) error
	CreateSigningKey(ctx context.Context, k SigningKey, audit AuditEvent) (time.Time, error)
	ActivateSigningKey(ctx context.Context, id string, lead time.Duration, audit AuditEvent) error
	SigningKeys(ctx context.Context) ([]SigningKey, error)
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

type AccountKey struct {
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
	LoadPage(ctx context.Context, after AccountKey, limit int) ([]DirectoryEntry, error)
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
