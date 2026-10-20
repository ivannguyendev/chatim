package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListMembers(ctx context.Context, req *chatimv1.ListMembersRequest) (*chatimv1.ListMembersResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	after, err := decodeMemberCursor(req.GetAfter())
	if err != nil {
		return nil, err
	}
	page, err := s.memberReader.ListMembers(ctx, who.Tenant, who.User, room, after, req.GetLimit())
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListMembersResponse{Members: make([]*chatimv1.MemberItem, len(page.Members))}
	for i, m := range page.Members {
		resp.Members[i] = &chatimv1.MemberItem{
			User: m.User, Role: pbconv.MemberRole(m.Role), Priority: m.Priority, JoinedAt: timestamppb.New(m.JoinedAt), ReadSeq: m.ReadSeq, ReadVer: m.ReadVer,
		}
	}
	if page.Next != nil {
		resp.Next = encodeMemberCursor(*page.Next)
	}
	return resp, nil
}
