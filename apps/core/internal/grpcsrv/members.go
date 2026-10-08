package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) AddMembers(ctx context.Context, req *chatimv1.AddMembersRequest) (*chatimv1.AddMembersResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	added, err := s.mutator.AddMembers(ctx, mutate.AddMembersCmd{
		Tenant: who.tenant, User: who.user, Room: room, Users: req.GetUsers(), RequestID: req.GetRequestId(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*chatimv1.AddedMember, len(added))
	for i, m := range added {
		out[i] = &chatimv1.AddedMember{User: m.User, Ver: m.Ver}
	}
	return &chatimv1.AddMembersResponse{Added: out}, nil
}

func (s *Service) RemoveMember(ctx context.Context, req *chatimv1.RemoveMemberRequest) (*chatimv1.RemoveMemberResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.RemoveMember(ctx, mutate.RemoveMemberCmd{Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.RemoveMemberResponse{Changed: res.Changed, Ver: res.Member.Ver}, nil
}

func (s *Service) LeaveRoom(ctx context.Context, req *chatimv1.LeaveRoomRequest) (*chatimv1.LeaveRoomResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.LeaveRoom(ctx, mutate.LeaveRoomCmd{Tenant: who.tenant, User: who.user, Room: room})
	if err != nil {
		return nil, err
	}
	return &chatimv1.LeaveRoomResponse{Changed: res.Changed, Ver: res.Member.Ver, NewOwner: res.Successor}, nil
}

func (s *Service) ChangeMemberRole(ctx context.Context, req *chatimv1.ChangeMemberRoleRequest) (*chatimv1.ChangeMemberRoleResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	role, err := pbconv.DomainMemberRole(req.GetRole())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.ChangeMemberRole(ctx, mutate.ChangeRoleCmd{Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser(), Role: role})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ChangeMemberRoleResponse{Changed: res.Changed, Ver: res.Member.Ver, PreviousRole: pbconv.MemberRole(res.PreviousRole)}, nil
}

func (s *Service) SetMemberPriority(ctx context.Context, req *chatimv1.SetMemberPriorityRequest) (*chatimv1.SetMemberPriorityResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.SetMemberPriority(ctx, mutate.SetPriorityCmd{
		Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser(), Priority: req.GetPriority(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.SetMemberPriorityResponse{Changed: res.Changed, Ver: res.Member.Ver, PreviousPriority: res.PreviousPriority}, nil
}
