package mutate

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const requestIDBytes = 17

type MemberStore interface {
	store.MemberWriter
	store.OwnerChanges
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	AddMemberCount(ctx context.Context, room uint64, delta int) (domain.MemberCount, error)
}

type RequestDedupe interface {
	Begin(ctx context.Context, k dedupe.Key) (dedupe.RequestStatus, error)
	Finish(ctx context.Context, k dedupe.Key, rec dedupe.Record)
	Cancel(ctx context.Context, k dedupe.Key)
}

type CountTimers interface {
	Arm(ctx context.Context, room uint64) (work.Timer, error)
	Disarm(ctx context.Context, t work.Timer)
}

type MemberForgetter interface {
	ForgetMembers(room uint64)
}

type AddMembersCmd struct {
	Tenant, User string
	Room         uint64
	Users        []string
	RequestID    string
}

type RemoveMemberCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
}

type LeaveRoomCmd struct {
	Tenant, User string
	Room         uint64
}

type ChangeRoleCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
	Role         domain.Role
}

type SetPriorityCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
	Priority     int32
}

type MemberResult struct {
	Member           domain.Member
	Changed          bool
	Successor        string
	PreviousRole     domain.Role
	PreviousPriority int32
}

func randomRequestID() string {
	var b [requestIDBytes]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
