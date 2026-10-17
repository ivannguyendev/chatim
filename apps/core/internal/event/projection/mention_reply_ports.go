package projection

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

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

type LinkTimers interface {
	ArmMessageCountCheck(ctx context.Context, key store.MsgKey, counter string) (timer.Timer, error)
	Disarm(ctx context.Context, tm timer.Timer)
}

type EventQueue interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type MemberLookup interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
}

type MentionReads interface {
	MarkRead(ctx context.Context, room uint64, user string, readSeq uint64) error
}
