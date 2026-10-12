package grpcsrv

import (
	"context"
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errBadForward       = fmt.Errorf("%w: forward_from", apperr.ErrInvalidArgument)
	errForwardWithReply = fmt.Errorf("%w: forward_from with reply_to", apperr.ErrInvalidArgument)
)

func (s *Service) forwardCmd(ctx context.Context, who caller, room uint64, req *chatimv1.SendMessageRequest) (actor.SendCmd, error) {
	if req.GetReplyTo() != nil {
		return actor.SendCmd{}, errForwardWithReply
	}
	key, err := forwardKey(req.GetForwardFrom())
	if err != nil {
		return actor.SendCmd{}, err
	}
	src, err := s.forwardSource(ctx, who, key)
	if err != nil {
		return actor.SendCmd{}, err
	}
	return actor.SendCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), CID: req.GetCid(), Text: src.Text,
		Forward: forwardOrigin(key, src),
	}, nil
}

func forwardKey(ref *chatimv1.ForwardRef) (store.MsgKey, error) {
	room, err := ids.ParseRoomID(ref.GetRoomId())
	if err != nil || ref.GetThreadRoot() != 0 || ref.GetSeq() == 0 || ref.GetSeq() == math.MaxUint64 {
		return store.MsgKey{}, errBadForward
	}
	return store.MsgKey{Room: room, Seq: ref.GetSeq()}, nil
}

func (s *Service) forwardSource(ctx context.Context, who caller, key store.MsgKey) (domain.Message, error) {
	grant, err := s.access.Admit(ctx, access.ForwardMessage, who.tenant, who.user, key.Room)
	if err != nil {
		return domain.Message{}, err
	}
	found, err := s.pages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.Message{}, err
	}
	if len(found) == 0 {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	src := found[0]
	if err := s.forwardable(ctx, grant.Member, key, src); err != nil {
		return domain.Message{}, err
	}
	grant.Author, grant.Kind = src.From, src.Kind
	return src, s.access.Allow(ctx, grant)
}

func (s *Service) forwardable(ctx context.Context, m domain.Member, key store.MsgKey, src domain.Message) error {
	if (view.Viewer{User: m.User, ClearedAt: m.ClearedAt}).Cleared(src.CreatedAt) {
		return domain.ErrMessageNotFound
	}
	_, hidden, err := s.hidden.Get(ctx, m.User, key)
	switch {
	case err != nil:
		return err
	case hidden:
		return domain.ErrMessageNotFound
	case src.Deleted:
		return domain.ErrMessageDeleted
	default:
		return nil
	}
}

func forwardOrigin(key store.MsgKey, src domain.Message) *domain.ForwardRef {
	if src.Forward != nil {
		origin := *src.Forward
		return &origin
	}
	return &domain.ForwardRef{Room: key.Room, Thread: key.Thread, Seq: key.Seq, Author: src.From, SentAt: src.CreatedAt}
}
