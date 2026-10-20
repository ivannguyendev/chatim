package message

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errReplyInThread = fmt.Errorf("%w: reply_to inside a thread", apperr.ErrInvalidArgument)

func (m *Editor) CheckReply(ctx context.Context, tenant, user string, room, thread uint64, reply *domain.ReplyRef) error {
	if reply == nil {
		return nil
	}
	if thread != 0 {
		return errReplyInThread
	}
	if err := domain.ValidateReply(reply); err != nil {
		return err
	}
	found, err := m.d.Messages.Find(ctx, room, []store.MsgKey{{Room: room, Thread: reply.Thread, Seq: reply.Seq}})
	if err != nil || len(found) > 0 {
		return err
	}
	if _, err := m.d.Access.Admit(ctx, access.SendMessage, tenant, user, room); err != nil {
		return err
	}
	return domain.ErrMessageNotFound
}
