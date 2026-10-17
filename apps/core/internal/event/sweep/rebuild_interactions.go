package sweep

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errDocGone = fmt.Errorf("%w: pending document is gone", apperr.ErrInvalidArgument)

type InteractionReader interface {
	GetReaction(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error)
	GetBookmark(ctx context.Context, key store.MsgKey, user string) (domain.Bookmark, bool, error)
}

type interactionRebuilder struct {
	interactions InteractionReader
	types        *eventkit.RoomTypes
}

func NewInteractionRebuilder(interactions InteractionReader, types *eventkit.RoomTypes) Rebuilder {
	return &interactionRebuilder{interactions: interactions, types: types}
}

func (r *interactionRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	if bit != store.BitInteraction {
		return nil, unknownBit(doc.Collection, bit)
	}
	key := eventkit.MessageKey(doc.Room, doc.Thread, doc.Seq)
	switch doc.Kind {
	case keys.ReactionKind:
		return r.reaction(ctx, doc, key)
	case keys.BookmarkKind:
		return r.bookmark(ctx, doc, key)
	default:
		return nil, fmt.Errorf("%w: interaction kind %d", apperr.ErrInvalidArgument, doc.Kind)
	}
}

func (r *interactionRebuilder) reaction(ctx context.Context, doc store.PendingDoc, key store.MsgKey) ([]*chatimv1.Event, error) {
	cur, found, err := r.interactions.GetReaction(ctx, key, doc.User)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, fmt.Errorf("reaction of %s on %+v: %w", doc.User, key, errDocGone)
	}
	typ, err := r.types.Type(ctx, doc.Room)
	if err != nil {
		return nil, dropWith(interactionTarget(doc, cur.N), err)
	}
	return one(pbconv.ReactionChanged(typ, cur)), nil
}

func (r *interactionRebuilder) bookmark(ctx context.Context, doc store.PendingDoc, key store.MsgKey) ([]*chatimv1.Event, error) {
	cur, found, err := r.interactions.GetBookmark(ctx, key, doc.User)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, fmt.Errorf("bookmark of %s on %+v: %w", doc.User, key, errDocGone)
	}
	room, err := r.types.Identity(ctx, doc.Room)
	if err != nil {
		return nil, dropWith(interactionTarget(doc, cur.Ver), err)
	}
	return one(pbconv.BookmarkChanged(room, cur)), nil
}

func interactionTarget(doc store.PendingDoc, ver uint32) store.StateTarget {
	return store.StateTarget{
		Collection: store.StateInteractions, Bit: store.BitInteraction, Room: doc.Room, Thread: doc.Thread, Seq: doc.Seq,
		User: doc.User, Kind: doc.Kind, Ver: uint64(ver),
	}
}
