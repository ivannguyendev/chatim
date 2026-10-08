package mutate

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type HideCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

type ClearCmd struct {
	Tenant, User string
	Room         uint64
}

func (m *Mutator) Hide(ctx context.Context, c HideCmd) error {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return err
	}
	req, _, err := m.target(ctx, access.HideMessage, c.Tenant, c.User, key)
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

func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (time.Time, error) {
	req, err := m.d.Access.Authorize(ctx, access.ClearHistory, c.Tenant, c.User, c.Room)
	if err != nil {
		return time.Time{}, err
	}
	now := m.now()
	at, rose, err := m.d.Rooms.ClearHistory(ctx, c.Room, c.User, now)
	if err != nil {
		return time.Time{}, err
	}
	if rose {
		m.tell(c.Room, pbconv.HistoryCleared(req.Room, c.User, at, now))
	}
	return at, nil
}
