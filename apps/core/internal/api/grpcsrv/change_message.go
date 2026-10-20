package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) EditMessage(ctx context.Context, req *chatimv1.EditMessageRequest) (*chatimv1.EditMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	m, err := s.editor.Edit(ctx, message.EditCmd{
		Tenant: who.Tenant, User: who.User, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(),
		BaseVersion: req.GetBaseVer(), Text: req.GetText(), Mentions: editMentions(req.GetMentions()),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.EditMessageResponse{Message: pbconv.Message(m)}, nil
}

func editMentions(set *chatimv1.MentionSet) *message.MentionSet {
	if set == nil {
		return nil
	}
	return &message.MentionSet{Targets: pbconv.DomainMentionTargets(set.GetTargets()), All: set.GetAll()}
}

func (s *Service) DeleteMessage(ctx context.Context, req *chatimv1.DeleteMessageRequest) (*chatimv1.DeleteMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	m, err := s.editor.Delete(ctx, message.DeleteCmd{
		Tenant: who.Tenant, User: who.User, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), BaseVersion: req.GetBaseVer(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.DeleteMessageResponse{Message: pbconv.Message(m)}, nil
}

func (s *Service) HideMessage(ctx context.Context, req *chatimv1.HideMessageRequest) (*chatimv1.HideMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	err = s.editor.Hide(ctx, message.HideCmd{Tenant: who.Tenant, User: who.User, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.HideMessageResponse{}, nil
}

func (s *Service) ClearHistory(ctx context.Context, req *chatimv1.ClearHistoryRequest) (*chatimv1.ClearHistoryResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	at, err := s.memberService.ClearHistory(ctx, member.ClearCmd{Tenant: who.Tenant, User: who.User, Room: room})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedAt: timestamppb.New(at)}, nil
}

func callerAndRoom(ctx context.Context, roomID string) (callerid.Caller, uint64, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return callerid.Caller{}, 0, err
	}
	room, err := parseRoomID(roomID)
	if err != nil {
		return callerid.Caller{}, 0, err
	}
	return who, room, nil
}
