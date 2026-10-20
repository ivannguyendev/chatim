package actor

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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
	if !a.dirty {
		return nil
	}
	seq, err := a.r.msgs.Last(ctx, a.id, 0)
	if err != nil {
		return fmt.Errorf("reload last seq: %w", err)
	}
	a.last = max(a.last, seq)
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
	if err := a.authorize(ctx, c); err != nil {
		q.answer(Ack{}, err)
		return nil
	}
	k := dedupeKey{user: c.User, cid: c.CID}
	if a.cache.join(k, q) || a.recheck(ctx, k) {
		return nil
	}
	return &entry{key: k, msg: c.message()}
}

func (a *actor) authorize(ctx context.Context, c SendCmd) error {
	if err := domain.CheckTenant(a.room, c.Tenant); err != nil {
		return err
	}
	if c.Kind == domain.KindSystem {
		return a.r.policy.Check(ctx, access.Request{Action: access.SendSystemMessage, User: c.User, Room: a.room})
	}
	m, err := a.member(ctx, c.User)
	if err != nil {
		return err
	}
	return a.allow(ctx, c, m)
}

func (a *actor) allow(ctx context.Context, c SendCmd, m domain.Member) error {
	req := access.Request{Action: access.SendMessage, User: c.User, Room: a.room, Member: m}
	if err := a.r.policy.Check(ctx, req); err != nil || !c.MentionAll {
		return err
	}
	req.Action = access.MentionAll
	return a.r.policy.Check(ctx, req)
}
