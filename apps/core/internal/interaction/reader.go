package interaction

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errMissingReaderDeps = fmt.Errorf("%w: interaction reader needs access, messages, message views, interactions, threads, pins and reaction settings", apperr.ErrInvalidArgument)

type MessageViews interface {
	Masked(ctx context.Context, user string, grant access.Request, room, thread uint64, found []domain.Message) ([]domain.Message, error)
	Viewed(ctx context.Context, user string, grant access.Request, room, thread uint64, found []domain.Message) ([]domain.Message, error)
	Mine(ctx context.Context, user string, page []domain.Message) ([]message.Item, error)
	VisibleMessages(ctx context.Context, tenant, user string, keys []store.MsgKey) (map[store.MsgKey]domain.Message, error)
}

type InteractionReads interface {
	Bookmarks(ctx context.Context, tenant, user string, before store.BookmarkCursor, limit int) ([]domain.Bookmark, error)
	Replies(ctx context.Context, parent store.MsgKey, afterSeq uint64, limit int) ([]domain.Reply, error)
	Reactors(ctx context.Context, q store.ReactorQuery) ([]store.Reactor, error)
}

type ThreadLister interface {
	List(ctx context.Context, room uint64, before store.ReplyThreadCursor, limit int) ([]store.ReplyThread, error)
}

type PinReader interface {
	Current(ctx context.Context, room uint64) (domain.PinState, error)
}

type ReactionSettings interface {
	ReactionEmojis() []string
}

type ReaderDeps struct {
	Access       *access.Checker
	Messages     Messages
	Views        MessageViews
	Interactions InteractionReads
	Threads      ThreadLister
	Pins         PinReader
	Settings     ReactionSettings
}

type Reader struct {
	d ReaderDeps
}

func NewReader(d ReaderDeps) (*Reader, error) {
	if d.Access == nil || d.Messages == nil || d.Views == nil || d.Interactions == nil || d.Threads == nil || d.Pins == nil || d.Settings == nil {
		return nil, errMissingReaderDeps
	}
	return &Reader{d: d}, nil
}

func byKey(msgs []domain.Message) map[store.MsgKey]domain.Message {
	out := make(map[store.MsgKey]domain.Message, len(msgs))
	for _, m := range msgs {
		out[store.KeyOf(m)] = m
	}
	return out
}
