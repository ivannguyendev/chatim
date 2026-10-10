package actor

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/backoff"
)

func (a *actor) settle(res []store.Result) {
	g := a.flight
	a.flight = nil
	defer g.cancel()
	if len(res) != len(g.entries) {
		a.r.log.ErrorContext(g.ctx, "write group result count mismatch", "room", a.id, "results", len(res), "messages", len(g.entries))
		res = make([]store.Result, len(g.entries))
	}
	var open []*entry
	for i, e := range g.entries {
		switch r := res[i]; {
		case r.Outcome == store.Inserted:
			a.commit(e, e.msg)
		case r.Outcome == store.Rejected:
			a.r.log.WarnContext(g.ctx, "message rejected by store", "room", a.id, "seq", e.msg.Seq, "err", r.Err)
			a.fail(e, r.Err, e.fixed)
		case r.Outcome == store.Duplicate:
			e.dup = true
			open = append(open, e)
		case !e.fixed && errors.Is(r.Err, flush.ErrNotSent):
			a.requeue(e, false, errWriteNotSent)
		default:
			e.dup = false
			open = append(open, e)
		}
	}
	a.conclude(g.ctx)
	a.reconcile(g.ctx, open)
	a.contended = false
	slices.SortFunc(a.retries, func(x, y *entry) int { return cmp.Compare(x.msg.Seq, y.msg.Seq) })
}

func (a *actor) reconcile(ctx context.Context, open []*entry) {
	wait := findBackoff
	for len(open) > 0 {
		found, err := a.r.msgs.Find(ctx, a.id, keysOf(open))
		if err == nil {
			open = a.resolve(open, found)
			a.abandonRetries()
			a.conclude(ctx)
		}
		if len(open) == 0 || !backoff.Pause(ctx, backoff.Jitter(wait)) {
			break
		}
		wait = min(2*wait, maxFindBackoff)
	}
	if len(open) > 0 {
		a.r.log.WarnContext(ctx, "message writes unconfirmed at group deadline", "room", a.id, "messages", len(open))
	}
	for _, e := range open {
		a.fail(e, errUnconfirmed, true)
	}
	a.conclude(ctx)
}

func (a *actor) resolve(open []*entry, found []domain.Message) []*entry {
	bySeq := make(map[uint64]domain.Message, len(found))
	for _, m := range found {
		bySeq[m.Seq] = m
	}
	var unresolved []*entry
	for _, e := range open {
		doc, ok := bySeq[e.msg.Seq]
		switch {
		case ok && doc.From == e.msg.From && doc.CID == e.msg.CID:
			a.commit(e, doc)
		case ok:
			a.contend(e)
		case e.dup:
			unresolved = append(unresolved, e)
		default:
			a.requeue(e, true, errUnconfirmed)
		}
	}
	return unresolved
}

func (a *actor) requeue(e *entry, fixed bool, exhausted error) {
	count := &e.unsent
	if fixed {
		count = &e.resends
	}
	switch {
	case *count >= maxRequeues:
		a.fail(e, exhausted, fixed)
	case !a.affordsAnotherCycle(e):
		a.fail(e, errOutOfTime, fixed)
	default:
		*count++
		e.fixed = fixed
		a.retries = append(a.retries, e)
	}
}

func (a *actor) affordsAnotherCycle(e *entry) bool {
	end := e.admittedAt.Add(a.r.cfg.ReservationTTL - reservationMargin)
	return !time.Now().Add(a.r.cfg.GroupDeadline).After(end)
}

func keysOf(entries []*entry) []store.MsgKey {
	out := make([]store.MsgKey, len(entries))
	for i, e := range entries {
		out[i] = store.KeyOf(e.msg)
	}
	return out
}
