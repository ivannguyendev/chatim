package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type MessageFinder interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type messageRebuilder struct {
	messages MessageFinder
	types    *eventkit.RoomTypes
	now      func() time.Time
}

func NewMessageRebuilder(messages MessageFinder, types *eventkit.RoomTypes, now func() time.Time) Rebuilder {
	if now == nil {
		now = time.Now
	}
	return &messageRebuilder{messages: messages, types: types, now: now}
}

func (r *messageRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	found, err := r.messages.Find(ctx, doc.Room, []store.MsgKey{eventkit.MessageKey(doc.Room, doc.Thread, doc.Seq)})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	}
	m := found[0]
	target := store.StateTarget{Collection: store.StateMessages, Bit: bit, Room: m.Room, Thread: m.Thread, Seq: m.Seq, Ver: messageStateVersion(m, bit)}
	typ, err := r.types.Type(ctx, m.Room)
	if err != nil {
		return nil, dropWith(target, err)
	}
	switch bit {
	case store.BitMsgCreated:
		return one(pbconv.MessageCreated(typ, m)), nil
	case store.BitReactionCounts:
		if m.Reactions.Version == 0 {
			return nil, dropWith(target, unversioned(pbconv.ReactionsCounter))
		}
		return one(pbconv.CountsChanged(typ, m, r.stamp())), nil
	case store.BitReplyCounts:
		if m.Replies.Version == 0 {
			return nil, dropWith(target, unversioned(pbconv.RepliesCounter))
		}
		return one(pbconv.ReplyCountsChanged(typ, m, r.stamp())), nil
	default:
		return nil, fmt.Errorf("%w: messages state bit %d", apperr.ErrInvalidArgument, bit)
	}
}

func (r *messageRebuilder) stamp() time.Time { return r.now().UTC().Truncate(time.Millisecond) }

func messageStateVersion(m domain.Message, bit store.StateBit) uint64 {
	switch bit {
	case store.BitReactionCounts:
		return m.Reactions.Version
	case store.BitReplyCounts:
		return m.Replies.Version
	default:
		return 0
	}
}

func unversioned(counter string) error {
	return fmt.Errorf("%w: %s count has no version", apperr.ErrInvalidArgument, counter)
}
