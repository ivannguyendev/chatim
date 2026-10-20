package interaction

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type PinnedMessage struct {
	Pin     domain.Pin
	Message domain.Message
}

type PinList struct {
	Version uint64
	Items   []PinnedMessage
}

func (r *Reader) Pins(ctx context.Context, tenant, user string, room uint64) (PinList, error) {
	grant, err := r.d.Access.Authorize(ctx, access.ReadHistory, tenant, user, room)
	if err != nil {
		return PinList{}, err
	}
	state, err := r.d.Pins.Current(ctx, room)
	if err != nil {
		return PinList{}, err
	}
	pins := slices.SortedFunc(slices.Values(state.Pins), func(a, b domain.Pin) int { return cmp.Compare(b.PV, a.PV) })
	shown, err := r.pinnedMessages(ctx, user, grant, pins)
	if err != nil {
		return PinList{}, err
	}
	list := PinList{Version: state.Version, Items: make([]PinnedMessage, len(pins))}
	for i, p := range pins {
		key := store.MsgKey{Room: room, Thread: p.Thread, Seq: p.Seq}
		m, ok := shown[key]
		if !ok {
			m = domain.Message{Room: room, Thread: key.Thread, Seq: key.Seq}
		}
		list.Items[i] = PinnedMessage{Pin: p, Message: m}
	}
	return list, nil
}

func (r *Reader) pinnedMessages(ctx context.Context, user string, grant access.Request, pins []domain.Pin) (map[store.MsgKey]domain.Message, error) {
	if len(pins) == 0 {
		return nil, nil
	}
	keys := make([]store.MsgKey, len(pins))
	for i, p := range pins {
		keys[i] = store.MsgKey{Room: grant.Room.ID, Thread: p.Thread, Seq: p.Seq}
	}
	found, err := r.d.Messages.Find(ctx, grant.Room.ID, keys)
	if err != nil {
		return nil, err
	}
	shown, err := r.d.Views.Masked(ctx, user, grant, grant.Room.ID, 0, found)
	if err != nil {
		return nil, err
	}
	return byKey(shown), nil
}
