package timer

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Events interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type MemberCounter interface {
	CountMembers(ctx context.Context, room uint64) (int, error)
	SetMemberCount(ctx context.Context, room, base uint64, count int) (domain.MemberCount, bool, error)
}

type MemberTimers interface {
	ArmMemberCountCheck(ctx context.Context, room uint64) (Timer, error)
}

type MessageFinder interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type MessageCountReader interface {
	CountReactions(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error)
	CountLiveReplies(ctx context.Context, parent store.MsgKey) (uint32, error)
}

type MessageCountWriter interface {
	SetReactions(ctx context.Context, key store.MsgKey, base uint64, s domain.ReactionSummary) (bool, error)
	SetReplyCount(ctx context.Context, key store.MsgKey, base uint64, n uint32) (bool, error)
}

type MessageTimers interface {
	ArmMessageCountCheck(ctx context.Context, key store.MsgKey, counter string) (Timer, error)
}

func stamp(now func() time.Time) time.Time { return now().UTC().Truncate(time.Millisecond) }
