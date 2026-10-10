package grpcsrv

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errReplyInThread = fmt.Errorf("%w: reply_to inside a thread", apperr.ErrInvalidArgument)

func (s *Service) sendCmd(ctx context.Context, who caller, room uint64, req *chatimv1.SendMessageRequest) (actor.SendCmd, error) {
	if req.GetForwardFrom() != nil {
		return s.forwardCmd(ctx, who, room, req)
	}
	reply := pbconv.DomainReplyRef(req.GetReplyTo())
	if err := s.checkReply(ctx, who, room, req.GetThreadRoot(), reply); err != nil {
		return actor.SendCmd{}, err
	}
	return actor.SendCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), CID: req.GetCid(), Text: req.GetText(),
		ReplyTo:    reply,
		Mentions:   pbconv.DomainMentionTargets(req.GetMentions().GetTargets()),
		MentionAll: req.GetMentions().GetAll(),
	}, nil
}

func (s *Service) checkReply(ctx context.Context, who caller, room, thread uint64, reply *domain.ReplyRef) error {
	if reply == nil {
		return nil
	}
	if thread != 0 {
		return errReplyInThread
	}
	if err := domain.ValidateReply(reply); err != nil {
		return err
	}
	found, err := s.pages.Find(ctx, room, []store.MsgKey{{Room: room, Thread: reply.Thread, Seq: reply.Seq}})
	if err != nil || len(found) > 0 {
		return err
	}
	if _, err := s.access.Admit(ctx, access.SendMessage, who.tenant, who.user, room); err != nil {
		return err
	}
	return domain.ErrMessageNotFound
}
