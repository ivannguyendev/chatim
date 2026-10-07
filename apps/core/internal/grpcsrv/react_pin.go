package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ReactMessage(ctx context.Context, req *chatimv1.ReactMessageRequest) (*chatimv1.ReactMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.React(ctx, mutate.ReactCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), Emoji: req.GetEmoji(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ReactMessageResponse{Change: res.Change, Reactions: pbconv.ReactionSummary(res.Reactions)}, nil
}

func (s *Service) PinMessage(ctx context.Context, req *chatimv1.PinMessageRequest) (*chatimv1.PinMessageResponse, error) {
	cmd, err := pinCmdOf(ctx, req.GetRoomId(), req.GetThreadRoot(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	state, err := s.mutator.Pin(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.PinMessageResponse{PinVer: state.Version, Pins: pbconv.Pins(state.Pins)}, nil
}

func (s *Service) UnpinMessage(ctx context.Context, req *chatimv1.UnpinMessageRequest) (*chatimv1.UnpinMessageResponse, error) {
	cmd, err := pinCmdOf(ctx, req.GetRoomId(), req.GetThreadRoot(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	state, err := s.mutator.Unpin(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.UnpinMessageResponse{PinVer: state.Version, Pins: pbconv.Pins(state.Pins)}, nil
}

func pinCmdOf(ctx context.Context, roomID string, thread, seq uint64) (mutate.PinCmd, error) {
	who, room, err := callerAndRoom(ctx, roomID)
	if err != nil {
		return mutate.PinCmd{}, err
	}
	return mutate.PinCmd{Tenant: who.tenant, User: who.user, Room: room, Thread: thread, Seq: seq}, nil
}
