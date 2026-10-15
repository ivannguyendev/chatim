package effects

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Marks interface {
	Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error)
}

type MessageFinder interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type EditReader interface {
	At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error)
}

type EditApplier interface {
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type TextPurger interface {
	PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error
}

type ReactionReader interface {
	GetReaction(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error)
}

type PinFacts interface {
	At(ctx context.Context, room, pv uint64) (domain.PinAction, error)
}

type PinProjecter interface {
	Project(ctx context.Context, room, target uint64) (domain.PinState, error)
}

type MemberLookup interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
}

type HiddenLookup interface {
	Get(ctx context.Context, user string, key store.MsgKey) (domain.HiddenMessage, bool, error)
}

type MessageCountTimers interface {
	ArmMessageCountCheck(ctx context.Context, key store.MsgKey, counter string) (timer.Timer, error)
}

type BookmarkReader interface {
	GetBookmark(ctx context.Context, key store.MsgKey, user string) (domain.Bookmark, bool, error)
}

type ReplyLinks interface {
	AddReply(ctx context.Context, r domain.Reply) (bool, error)
	RemoveReply(ctx context.Context, r domain.Reply, at time.Time) (bool, error)
}

type ReplyThreadWriter interface {
	Touch(ctx context.Context, parent store.MsgKey, tenant string, replySeq uint64, at time.Time) error
}

type ReplyCounter interface {
	AddReplyCount(ctx context.Context, key store.MsgKey, delta int) (domain.ReplyCount, error)
}

type MentionWriter interface {
	ApplyMentions(ctx context.Context, s store.MentionSet) error
}

type SharedMentionRaiser interface {
	RaiseSharedMention(ctx context.Context, room uint64, at time.Time) error
}

type MentionReads interface {
	MarkRead(ctx context.Context, room uint64, user string, readSeq uint64) error
}

type LinkTimers interface {
	MessageCountTimers
	Disarm(ctx context.Context, tm timer.Timer)
}
