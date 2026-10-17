package sweep

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type PinFacts interface {
	At(ctx context.Context, room, pv uint64) (domain.PinAction, error)
}

type PinProjecter interface {
	Project(ctx context.Context, room, target uint64) (domain.PinState, error)
}

type pinRebuilder struct {
	projector PinProjecter
	pins      PinFacts
	messages  MessageFinder
	types     *eventkit.RoomTypes
}

func NewPinRebuilder(projector PinProjecter, pins PinFacts, messages MessageFinder, types *eventkit.RoomTypes) Rebuilder {
	return &pinRebuilder{projector: projector, pins: pins, messages: messages, types: types}
}

func (r *pinRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	if bit != store.BitPinFact {
		return nil, unknownBit(doc.Collection, bit)
	}
	if _, err := r.projector.Project(ctx, doc.Room, doc.Seq); err != nil {
		return nil, err
	}
	fact, err := r.pins.At(ctx, doc.Room, doc.Seq)
	switch {
	case errors.Is(err, store.ErrPinNotFound):
		return nil, fmt.Errorf("%w: %w", errDocGone, err)
	case err != nil:
		return nil, err
	}
	key := eventkit.MessageKey(fact.Room, fact.Thread, fact.Seq)
	found, err := r.messages.Find(ctx, fact.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	}
	typ, err := r.types.Type(ctx, doc.Room)
	if err != nil {
		return nil, err
	}
	return one(pbconv.PinChanged(typ, found[0], fact)), nil
}
