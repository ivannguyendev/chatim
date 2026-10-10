package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const MaxInteractionScan = 1000

var ErrStaleRead = fmt.Errorf("read is behind a witnessed write: %w", apperr.ErrUnavailable)

type Witness struct {
	User string
	N    uint32
}

type Interaction struct {
	Kind  keys.InteractionKind
	Key   MsgKey
	User  string
	Ver   uint32
	At    time.Time
	Reply MsgKey
}

type BookmarkCursor struct {
	At  time.Time
	Key MsgKey
}

type Interactions interface {
	SetReaction(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error)
	RemoveReaction(ctx context.Context, key MsgKey, user string, at time.Time) (domain.Reaction, bool, error)
	GetReaction(ctx context.Context, key MsgKey, user string) (domain.Reaction, bool, error)
	CountReactions(ctx context.Context, key MsgKey) ([]domain.ReactionCount, error)
	CountWitnessed(ctx context.Context, key MsgKey, witnesses []Witness) ([]domain.ReactionCount, error)
	SetBookmark(ctx context.Context, b domain.Bookmark) (domain.Bookmark, bool, error)
	GetBookmark(ctx context.Context, key MsgKey, user string) (domain.Bookmark, bool, error)
	Bookmarks(ctx context.Context, tenant, user string, before BookmarkCursor, limit int) ([]domain.Bookmark, error)
	AddReply(ctx context.Context, r domain.Reply) (bool, error)
	RemoveReply(ctx context.Context, parent, reply MsgKey, at time.Time) (bool, error)
	Replies(ctx context.Context, parent MsgKey, afterSeq uint64, limit int) ([]domain.Reply, error)
	CountLiveReplies(ctx context.Context, parent MsgKey) (uint32, error)
	Between(ctx context.Context, room uint64, kind keys.InteractionKind, from, to time.Time, limit int) ([]Interaction, error)
}

type ReactionSummaries interface {
	SetReactions(ctx context.Context, key MsgKey, base uint64, s domain.ReactionSummary) (bool, error)
}
