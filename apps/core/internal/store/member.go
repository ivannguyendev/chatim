package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const MaxMemberScan = 1000

type JoinResult struct {
	Members []domain.Member
	Changed int
}

type MemberWriter interface {
	AddMembers(ctx context.Context, j domain.Join, users []string) (JoinResult, error)
	ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error)
}

type MemberReader interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error)
}

type OwnerView struct {
	OwnersVer  uint64
	Docs       []domain.Member
	Owners     []domain.Member
	Candidates []domain.Member
}

type MemberWrite struct {
	Cur, Next domain.Member
}

type OwnerDecision func(OwnerView) ([]MemberWrite, error)

type OwnerResult struct {
	Written      []domain.Member
	Count        domain.MemberCount
	CountChanged bool
}

type OwnerChanges interface {
	ChangeOwners(ctx context.Context, room uint64, users []string, decide OwnerDecision) (OwnerResult, error)
}

type MemberCounts interface {
	AddMemberCount(ctx context.Context, room uint64, delta int) (domain.MemberCount, error)
	CountMembers(ctx context.Context, room uint64) (int, error)
	SetMemberCount(ctx context.Context, room, base uint64, count int) (domain.MemberCount, bool, error)
}

type ReadPositions interface {
	MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error)
	MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error)
}

func MemberCountDelta(writes []MemberWrite) int {
	delta := 0
	for _, w := range writes {
		switch {
		case w.Cur.Active() && !w.Next.Active():
			delta--
		case !w.Cur.Active() && w.Next.Active():
			delta++
		}
	}
	return delta
}
