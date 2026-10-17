package sweep

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type HiddenLookup interface {
	Get(ctx context.Context, user string, key store.MsgKey) (domain.HiddenMessage, bool, error)
}

type hiddenRebuilder struct {
	hidden HiddenLookup
	types  *eventkit.RoomTypes
}

func NewHiddenRebuilder(hidden HiddenLookup, types *eventkit.RoomTypes) Rebuilder {
	return &hiddenRebuilder{hidden: hidden, types: types}
}

func (r *hiddenRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	if bit != store.BitHidden {
		return nil, unknownBit(doc.Collection, bit)
	}
	key := eventkit.MessageKey(doc.Room, doc.Thread, doc.Seq)
	h, found, err := r.hidden.Get(ctx, doc.User, key)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, fmt.Errorf("hide of %+v by %s: %w", key, doc.User, errDocGone)
	}
	room, err := r.types.Identity(ctx, doc.Room)
	if err != nil {
		return nil, err
	}
	return one(pbconv.MessageHidden(room, doc.User, doc.Thread, doc.Seq, h.At)), nil
}
