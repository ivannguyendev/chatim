package grpcsrv

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type PinReader interface {
	Current(ctx context.Context, room uint64) (domain.PinState, error)
}

func (s *Service) ListPins(ctx context.Context, req *chatimv1.ListPinsRequest) (*chatimv1.ListPinsResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	grant, err := s.access.Authorize(ctx, access.ReadHistory, who.tenant, who.user, room)
	if err != nil {
		return nil, err
	}
	state, err := s.pinBoard.Current(ctx, room)
	if err != nil {
		return nil, err
	}
	pins := slices.SortedFunc(slices.Values(state.Pins), func(a, b domain.Pin) int { return cmp.Compare(b.PV, a.PV) })
	shown, err := s.pinnedMessages(ctx, who.user, grant, pins)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListPinsResponse{PinVer: state.Version, Items: make([]*chatimv1.PinnedMessage, len(pins))}
	for i, p := range pbconv.Pins(pins) {
		key := store.MsgKey{Room: room, Thread: pins[i].Thread, Seq: pins[i].Seq}
		m, ok := shown[key]
		if !ok {
			m = domain.Message{Room: room, Thread: key.Thread, Seq: key.Seq}
		}
		resp.Items[i] = &chatimv1.PinnedMessage{Pin: p, Message: pbconv.Message(m)}
	}
	return resp, nil
}

func (s *Service) pinnedMessages(ctx context.Context, user string, grant access.Request, pins []domain.Pin) (map[store.MsgKey]domain.Message, error) {
	if len(pins) == 0 {
		return nil, nil
	}
	keys := make([]store.MsgKey, len(pins))
	for i, p := range pins {
		keys[i] = store.MsgKey{Room: grant.Room.ID, Thread: p.Thread, Seq: p.Seq}
	}
	found, err := s.pages.Find(ctx, grant.Room.ID, keys)
	if err != nil {
		return nil, err
	}
	viewer, err := s.viewerOf(ctx, user, grant, store.PageQuery{Room: grant.Room.ID}, found)
	if err != nil {
		return nil, err
	}
	return byKey(s.maskView.Apply(viewer, found)), nil
}
