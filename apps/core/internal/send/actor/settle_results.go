package actor

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
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
			a.fail(e, r.Err, false)
		case r.Outcome == store.Unknown && errors.Is(r.Err, flush.ErrNotSent):
			a.fail(e, errGroupExpired, false)
		default:
			open = append(open, e)
		}
	}
	a.conclude(g.ctx)
	a.reconcile(g.ctx, open)
	a.contended = false
}

func (a *actor) reconcile(ctx context.Context, open []*entry) {
	if len(open) == 0 {
		return
	}
	found, err := a.r.msgs.Find(ctx, a.id, keysOf(open))
	if err != nil {
		a.r.log.WarnContext(ctx, "find of unconfirmed message writes failed", "room", a.id, "messages", len(open), "err", err)
	} else {
		open = a.resolve(open, found)
	}
	if len(open) > 0 {
		a.r.log.WarnContext(ctx, "message writes unconfirmed", "room", a.id, "messages", len(open))
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
		default:
			unresolved = append(unresolved, e)
		}
	}
	return unresolved
}

func keysOf(entries []*entry) []store.MsgKey {
	out := make([]store.MsgKey, len(entries))
	for i, e := range entries {
		out[i] = store.KeyOf(e.msg)
	}
	return out
}
