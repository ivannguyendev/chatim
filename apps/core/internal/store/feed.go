package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrFeedHistoryLost = fmt.Errorf("change feed history lost: %w", apperr.ErrUnavailable)
	ErrFeedBusy        = fmt.Errorf("change feed held by another consumer: %w", apperr.ErrUnavailable)
	ErrCorruptChange   = fmt.Errorf("change feed returned a corrupt change: %w", apperr.ErrFailedPrecondition)
)

type Position []byte

type ChangeKind uint8

const (
	MessageInserted ChangeKind = iota + 1
	RoomInserted
	EditInserted
	ReactionChanged
	PinInserted
	MemberChanged
	ReadChanged
	MessageHidden
	HistoryCleared
	MemberCountCheck
	BookmarkChanged
	MessageCountCheck
)

type ReplyMentionFlags uint32

const (
	HasReply ReplyMentionFlags = 1 << iota
	HasMention
)

func ReplyMentionFlagsOf(m domain.Message) ReplyMentionFlags {
	var f ReplyMentionFlags
	if m.ReplyTo != nil {
		f |= HasReply
	}
	if len(m.Mentions) > 0 || m.MentionAll {
		f |= HasMention
	}
	return f
}

type Change struct {
	Kind              ChangeKind
	Msg               domain.Message
	ReplyMentionFlags ReplyMentionFlags
	Room              domain.Room
	Edit              domain.Edit
	Reaction          domain.Reaction
	Bookmark          domain.Bookmark
	Pin               domain.PinAction
	Member            domain.Member
	Hidden            domain.HiddenMessage
	CommittedAt       time.Time
	Position          Position
}

type ChangeFeed interface {
	Open(ctx context.Context) (Cursor, error)
	Forget(ctx context.Context) error
}

type Cursor interface {
	Next(ctx context.Context) (Change, error)
	Confirm(ctx context.Context, pos Position) error
	Close(ctx context.Context) error
}
