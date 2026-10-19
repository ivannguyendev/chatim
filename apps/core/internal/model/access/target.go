package access

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type MessageFinder interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

func (c *Checker) Target(ctx context.Context, finder MessageFinder, action Action, tenant, user string, key store.MsgKey) (Request, domain.Message, error) {
	req, err := c.Admit(ctx, action, tenant, user, key.Room)
	if err != nil {
		return Request{}, domain.Message{}, err
	}
	found, err := finder.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return Request{}, domain.Message{}, err
	}
	if len(found) == 0 {
		return Request{}, domain.Message{}, domain.ErrMessageNotFound
	}
	msg := found[0]
	req.Author = msg.From
	req.Kind = msg.Kind
	if err := c.Allow(ctx, req); err != nil {
		return Request{}, domain.Message{}, err
	}
	return req, msg, nil
}
