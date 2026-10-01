package actor

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
)

func (a *actor) dispatch(ctx context.Context, first *request) {
	gctx, cancel := context.WithTimeout(ctx, a.r.cfg.GroupDeadline)
	entries := a.gather(gctx, first)
	if len(entries) == 0 {
		a.conclude(gctx)
		cancel()
		return
	}
	a.number(entries, time.Now().UTC().Truncate(time.Millisecond))
	msgs := make([]domain.Message, len(entries))
	for i, e := range entries {
		msgs[i] = e.msg
	}
	deadline, _ := gctx.Deadline()
	if err := a.r.sub.Submit(gctx, flush.Group{Room: a.id, Msgs: msgs, Done: a.deliver, Deadline: deadline}); err != nil {
		switch {
		case ctx.Err() != nil:
			err = errStopped
		case gctx.Err() != nil:
			err = errGroupExpired
		}
		cancel()
		for _, e := range entries {
			a.fail(e, err, e.fixed)
		}
		a.conclude(gctx)
		return
	}
	a.flight = &group{ctx: gctx, cancel: cancel, entries: entries}
}

func (a *actor) gather(ctx context.Context, first *request) []*entry {
	retries := a.retries
	a.retries = nil
	if err := a.refresh(ctx); err != nil {
		a.r.log.WarnContext(ctx, "reload room timeline failed", "room", a.id, "err", err)
		for _, e := range retries {
			a.fail(e, errUnavailable, e.fixed)
		}
		a.reject(first, errUnavailable)
		return nil
	}
	var fresh []*entry
	if first != nil {
		fresh = a.take(ctx, fresh, first)
	}
	fresh = a.drain(ctx, fresh, a.r.cfg.MaxGroup-len(retries))
	return append(retries, a.reserve(ctx, fresh)...)
}

func (a *actor) drain(ctx context.Context, fresh []*entry, limit int) []*entry {
	for len(fresh) < limit {
		select {
		case q := <-a.mailbox:
			fresh = a.take(ctx, fresh, q)
		default:
			return fresh
		}
	}
	return fresh
}

func (a *actor) take(ctx context.Context, entries []*entry, q *request) []*entry {
	if e := a.admit(ctx, q); e != nil {
		return append(entries, e)
	}
	return entries
}

func (a *actor) reject(first *request, err error) {
	if first != nil {
		first.answer(Ack{}, err)
	}
	for range a.r.cfg.MaxGroup {
		select {
		case q := <-a.mailbox:
			q.answer(Ack{}, err)
		default:
			return
		}
	}
}

func (a *actor) number(entries []*entry, now time.Time) {
	next := a.last
	for _, e := range entries {
		if e.fixed {
			next = max(next, e.msg.Seq)
		}
	}
	for _, e := range entries {
		if e.fixed {
			continue
		}
		next++
		e.msg.Seq, e.msg.Pts, e.msg.CreatedAt = next, next, now
	}
}
