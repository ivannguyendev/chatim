package mutate

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type HideCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

type ClearCmd struct {
	Tenant, User string
	Room         uint64
	UpToSeq      uint64
}

func (m *Mutator) Hide(ctx context.Context, c HideCmd) error {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return err
	}
	if _, _, err := m.target(ctx, access.HideMessage, c.Tenant, c.User, key); err != nil {
		return err
	}
	return m.d.Hidden.Hide(ctx, c.User, key)
}

func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (uint64, error) {
	if _, err := m.d.Access.Authorize(ctx, access.ClearHistory, c.Tenant, c.User, c.Room); err != nil {
		return 0, err
	}
	last, err := m.d.Messages.Last(ctx, c.Room, 0)
	if err != nil {
		return 0, err
	}
	seq := c.UpToSeq
	if seq == 0 || seq > last {
		seq = last
	}
	return m.d.Rooms.ClearHistory(ctx, c.Room, c.User, seq)
}
