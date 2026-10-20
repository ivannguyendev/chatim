package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) MarkRead(ctx context.Context, req *chatimv1.MarkReadRequest) (*chatimv1.MarkReadResponse, error) {
	cmd, err := readCmdOf(ctx, req.GetRoomId(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	pos, err := s.memberService.MarkRead(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.MarkReadResponse{ReadSeq: pos.Seq, ReadVer: pos.Ver}, nil
}

func (s *Service) MarkUnread(ctx context.Context, req *chatimv1.MarkUnreadRequest) (*chatimv1.MarkUnreadResponse, error) {
	cmd, err := readCmdOf(ctx, req.GetRoomId(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	pos, err := s.memberService.MarkUnread(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.MarkUnreadResponse{ReadSeq: pos.Seq, ReadVer: pos.Ver}, nil
}

func readCmdOf(ctx context.Context, roomID string, seq uint64) (member.ReadCmd, error) {
	who, room, err := callerAndRoom(ctx, roomID)
	if err != nil {
		return member.ReadCmd{}, err
	}
	return member.ReadCmd{Tenant: who.Tenant, User: who.User, Room: room, Seq: seq}, nil
}
