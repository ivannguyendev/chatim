package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListPins(ctx context.Context, req *chatimv1.ListPinsRequest) (*chatimv1.ListPinsResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	list, err := s.interactionReader.Pins(ctx, who.Tenant, who.User, room)
	if err != nil {
		return nil, err
	}
	pins := make([]domain.Pin, len(list.Items))
	for i, it := range list.Items {
		pins[i] = it.Pin
	}
	resp := &chatimv1.ListPinsResponse{PinVer: list.Version, Items: make([]*chatimv1.PinnedMessage, len(pins))}
	for i, p := range pbconv.Pins(pins) {
		resp.Items[i] = &chatimv1.PinnedMessage{Pin: p, Message: pbconv.Message(list.Items[i].Message)}
	}
	return resp, nil
}
