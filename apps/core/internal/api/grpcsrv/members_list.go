package grpcsrv

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const defaultMemberPage = 500

var errMemberLimit = fmt.Errorf("%w: limit", apperr.ErrInvalidArgument)

type MemberLister interface {
	ListMembers(ctx context.Context, room uint64, after store.MemberCursor, limit int) ([]domain.Member, error)
}

func (s *Service) ListMembers(ctx context.Context, req *chatimv1.ListMembersRequest) (*chatimv1.ListMembersResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	after, err := decodeMemberCursor(req.GetAfter())
	if err != nil {
		return nil, err
	}
	limit, err := memberPageLimit(req.GetLimit())
	if err != nil {
		return nil, err
	}
	if _, err := s.access.Authorize(ctx, access.ReadMembers, who.tenant, who.user, room); err != nil {
		return nil, err
	}
	members, err := s.memberList.ListMembers(ctx, room, after, limit)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListMembersResponse{Members: make([]*chatimv1.MemberItem, len(members))}
	for i, m := range members {
		resp.Members[i] = &chatimv1.MemberItem{
			User: m.User, Role: pbconv.MemberRole(m.Role), Priority: m.Priority, JoinedAt: timestamppb.New(m.JoinedAt), ReadSeq: m.ReadSeq, ReadVer: m.ReadVer,
		}
	}
	if len(members) == limit {
		resp.Next = encodeMemberCursor(members[len(members)-1])
	}
	return resp, nil
}

func memberPageLimit(n uint32) (int, error) {
	switch {
	case n == 0:
		return defaultMemberPage, nil
	case n > store.MaxMemberPage:
		return 0, errMemberLimit
	default:
		return int(n), nil
	}
}
