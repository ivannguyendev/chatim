package sweep

import (
	"context"
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errProjectionBehind = fmt.Errorf("message projection behind its edit fact: %w", apperr.ErrUnavailable)

type EditFacts interface {
	At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error)
	PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error
}

type EditedMessages interface {
	MessageFinder
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type editRebuilder struct {
	edits    EditFacts
	messages EditedMessages
	types    *eventkit.RoomTypes
}

func NewEditRebuilder(edits EditFacts, messages EditedMessages, types *eventkit.RoomTypes) Rebuilder {
	return &editRebuilder{edits: edits, messages: messages, types: types}
}

func (r *editRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	if bit != store.BitEditFact {
		return nil, unknownBit(doc.Collection, bit)
	}
	if doc.Ver == 0 || doc.Ver > math.MaxUint32 {
		return nil, fmt.Errorf("%w: edit fact version %d", apperr.ErrInvalidArgument, doc.Ver)
	}
	key := eventkit.MessageKey(doc.Room, doc.Thread, doc.Seq)
	fact, err := r.edits.At(ctx, key, uint32(doc.Ver))
	if err != nil {
		return nil, err
	}
	if err := r.project(ctx, key, fact); err != nil {
		return nil, err
	}
	found, err := r.messages.Find(ctx, doc.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	case found[0].Version < fact.Version:
		return nil, errProjectionBehind
	}
	previous, err := store.PreviousMentions(ctx, r.edits, found[0], fact)
	if err != nil {
		return nil, err
	}
	typ, err := r.types.Type(ctx, doc.Room)
	if err != nil {
		return nil, err
	}
	return one(pbconv.MessageChanged(typ, found[0], fact, previous)), nil
}

func (r *editRebuilder) project(ctx context.Context, key store.MsgKey, fact domain.Edit) error {
	if err := r.messages.ApplyEdit(ctx, fact); err != nil {
		return err
	}
	if fact.Kind != domain.EditDelete {
		return nil
	}
	return r.edits.PurgeText(ctx, key, fact.Version-1)
}

func unknownBit(c store.StateCollection, bit store.StateBit) error {
	return fmt.Errorf("%w: %s state bit %d", apperr.ErrInvalidArgument, c, bit)
}
