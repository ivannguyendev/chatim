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
		cancel()
		return
	}
	a.number(entries, time.Now().UTC().Truncate(time.Millisecond))
	msgs := make([]domain.Message, len(entries))
	for i, e := range entries {
		msgs[i] = e.msg
	}
	if err := a.r.sub.Submit(gctx, flush.Group{Room: a.id, Msgs: msgs, Done: a.deliver}); err != nil {
		cancel()
		if ctx.Err() != nil {
			err = errStopped
		}
		for _, e := range entries {
			a.fail(e, err, e.fixed)
		}
		return
	}
	a.flight = &group{ctx: gctx, cancel: cancel, entries: entries}
}

func (a *actor) gather(ctx context.Context, first *request) []*entry {
	entries := a.retries
	a.retries = nil
	if err := a.refresh(ctx); err != nil {
		a.r.log.WarnContext(ctx, "reload room timeline failed", "room", a.id, "err", err)
		for _, e := range entries {
			a.fail(e, errUnavailable, e.fixed)
		}
		a.reject(first, errUnavailable)
		return nil
	}
	if first != nil {
		entries = a.take(ctx, entries, first)
	}
	for len(entries) < a.r.cfg.MaxGroup {
		select {
		case q := <-a.mailbox:
			entries = a.take(ctx, entries, q)
		default:
			return entries
		}
	}
	return entries
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
