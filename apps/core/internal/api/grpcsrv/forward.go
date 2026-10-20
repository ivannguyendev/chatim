package grpcsrv

import (
	"context"
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errBadForward       = fmt.Errorf("%w: forward_from", apperr.ErrInvalidArgument)
	errForwardWithReply = fmt.Errorf("%w: forward_from with reply_to", apperr.ErrInvalidArgument)
)

func (s *Service) forwardCmd(ctx context.Context, who callerid.Caller, room uint64, req *chatimv1.SendMessageRequest) (actor.SendCmd, error) {
	if req.GetReplyTo() != nil {
		return actor.SendCmd{}, errForwardWithReply
	}
	key, err := forwardKey(req.GetForwardFrom())
	if err != nil {
		return actor.SendCmd{}, err
	}
	src, err := s.editor.Forward(ctx, who.Tenant, who.User, key)
	if err != nil {
		return actor.SendCmd{}, err
	}
	return actor.SendCmd{
		Tenant: who.Tenant, User: who.User, Room: room, Thread: req.GetThreadRoot(), CID: req.GetCid(), Text: src.Text,
		Forward: src.Origin,
	}, nil
}

func forwardKey(ref *chatimv1.ForwardRef) (store.MsgKey, error) {
	room, err := ids.ParseRoomID(ref.GetRoomId())
	if err != nil || ref.GetThreadRoot() != 0 || ref.GetSeq() == 0 || ref.GetSeq() == math.MaxUint64 {
		return store.MsgKey{}, errBadForward
	}
	return store.MsgKey{Room: room, Seq: ref.GetSeq()}, nil
}
