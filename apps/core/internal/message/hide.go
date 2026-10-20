package message

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type HideCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

func (m *Editor) Hide(ctx context.Context, c HideCmd) error {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return err
	}
	req, _, err := m.d.Access.Target(ctx, m.d.Messages, access.HideMessage, c.Tenant, c.User, key)
	if err != nil {
		return err
	}
	now := m.now()
	fresh, err := m.d.Hidden.Hide(ctx, c.User, key, now)
	if err != nil {
		return err
	}
	if fresh {
		m.tell(c.Room, pbconv.MessageHidden(req.Room, c.User, c.Thread, c.Seq, now))
	}
	return nil
}
