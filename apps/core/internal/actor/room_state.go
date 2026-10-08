package actor

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (a *actor) load(ctx context.Context) error {
	lctx, cancel := context.WithTimeout(ctx, a.r.cfg.GroupDeadline)
	defer cancel()
	room, err := a.r.rooms.Get(lctx, a.id)
	switch {
	case errors.Is(err, domain.ErrRoomNotFound):
		return domain.ErrRoomNotFound
	case err != nil:
		a.r.log.WarnContext(ctx, "load room failed", "room", a.id, "err", err)
		return errUnavailable
	}
	a.room = room
	if err := a.refresh(lctx); err != nil {
		a.r.log.WarnContext(ctx, "load room timeline failed", "room", a.id, "err", err)
		return errUnavailable
	}
	return nil
}

func (a *actor) refresh(ctx context.Context) error {
	if !a.stale && !a.dirty {
		return nil
	}
	seq, err := a.r.msgs.Last(ctx, a.id, 0)
	if err != nil {
		return fmt.Errorf("reload last seq: %w", err)
	}
	a.last, a.stale = max(a.last, seq), false
	if !a.dirty {
		return nil
	}
	page, err := a.r.msgs.Page(ctx, store.PageQuery{Room: a.id, Anchor: store.Latest, Limit: seedSize})
	if err != nil {
		return fmt.Errorf("seed cid cache: %w", err)
	}
	for _, m := range page {
		a.cache.seed(dedupeKey{user: m.From, cid: m.CID}, ackOf(m))
	}
	a.dirty = false
	return nil
}

func (a *actor) admit(ctx context.Context, q *request) *entry {
	c := q.cmd
	if err := domain.CheckTenant(a.room, c.Tenant); err != nil {
		q.answer(Ack{}, err)
		return nil
	}
	m, err := a.member(ctx, c.User)
	if err != nil {
		q.answer(Ack{}, err)
		return nil
	}
	if err := a.r.policy.Check(ctx, access.Request{Action: access.SendMessage, User: c.User, Room: a.room, Member: m}); err != nil {
		q.answer(Ack{}, err)
		return nil
	}
	k := dedupeKey{user: c.User, cid: c.CID}
	if a.cache.join(k, q) {
		return nil
	}
	return &entry{key: k, msg: domain.Message{
		Room:   a.id,
		Tenant: a.room.Tenant,
		From:   c.User,
		Kind:   domain.KindText,
		Text:   c.Text,
		CID:    c.CID,
	}}
}
