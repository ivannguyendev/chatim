package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) sendCmd(ctx context.Context, who callerid.Caller, room uint64, req *chatimv1.SendMessageRequest) (actor.SendCmd, error) {
	kind, err := sendKind(req)
	if err != nil {
		return actor.SendCmd{}, err
	}
	cmd, err := s.linkedCmd(ctx, who, room, req)
	if err != nil {
		return actor.SendCmd{}, err
	}
	cmd.Kind, cmd.SkipUnread, cmd.Meta = kind, req.GetSkipUnread(), req.GetMeta()
	return cmd, nil
}

func sendKind(req *chatimv1.SendMessageRequest) (domain.Kind, error) {
	kind, err := pbconv.DomainMessageKind(req.GetKind())
	if err != nil {
		return 0, err
	}
	links := domain.Message{
		Kind:       kind,
		ReplyTo:    pbconv.DomainReplyRef(req.GetReplyTo()),
		Mentions:   pbconv.DomainMentionTargets(req.GetMentions().GetTargets()),
		MentionAll: req.GetMentions().GetAll(),
	}
	if req.GetForwardFrom() != nil {
		links.Forward = &domain.ForwardRef{}
	}
	if err := domain.ValidateSystemMessage(links); err != nil {
		return 0, err
	}
	return kind, domain.ValidateMeta(req.GetMeta())
}

func (s *Service) linkedCmd(ctx context.Context, who callerid.Caller, room uint64, req *chatimv1.SendMessageRequest) (actor.SendCmd, error) {
	if req.GetForwardFrom() != nil {
		return s.forwardCmd(ctx, who, room, req)
	}
	reply := pbconv.DomainReplyRef(req.GetReplyTo())
	if err := s.editor.CheckReply(ctx, who.Tenant, who.User, room, req.GetThreadRoot(), reply); err != nil {
		return actor.SendCmd{}, err
	}
	return actor.SendCmd{
		Tenant: who.Tenant, User: who.User, Room: room, Thread: req.GetThreadRoot(), CID: req.GetCid(), Text: req.GetText(),
		ReplyTo:    reply,
		Mentions:   pbconv.DomainMentionTargets(req.GetMentions().GetTargets()),
		MentionAll: req.GetMentions().GetAll(),
	}, nil
}
