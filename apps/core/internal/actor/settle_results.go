package actor

import (
	"cmp"
	"context"
	"crypto/rand"
	"math/big"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
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
		switch r := res[i]; r.Outcome {
		case store.Inserted:
			a.commit(e, e.msg)
		case store.Rejected:
			a.r.log.WarnContext(g.ctx, "message rejected by store", "room", a.id, "seq", e.msg.Seq, "err", r.Err)
			a.fail(e, r.Err, e.fixed)
		case store.Duplicate:
			e.dup = true
			open = append(open, e)
		default:
			e.dup = false
			open = append(open, e)
		}
	}
	a.reconcile(g.ctx, open)
	slices.SortFunc(a.retries, func(x, y *entry) int { return cmp.Compare(x.msg.Seq, y.msg.Seq) })
}

func (a *actor) reconcile(ctx context.Context, open []*entry) {
	wait := findBackoff
	for len(open) > 0 {
		found, err := a.r.msgs.Find(ctx, a.id, keysOf(open))
		if err == nil {
			open = a.resolve(open, found)
		}
		if len(open) == 0 || !pause(ctx, jitter(wait)) {
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
			a.stale = true
			a.requeue(e, false)
		case e.dup:
			unresolved = append(unresolved, e)
		default:
			a.requeue(e, true)
		}
	}
	return unresolved
}

func (a *actor) requeue(e *entry, fixed bool) {
	count, exhausted := &e.reassigns, errSeqContention
	if fixed {
		count, exhausted = &e.resends, errUnconfirmed
	}
	if *count >= maxRequeues {
		a.fail(e, exhausted, fixed)
		return
	}
	*count++
	e.fixed = fixed
	a.retries = append(a.retries, e)
}

func keysOf(entries []*entry) []store.MsgKey {
	out := make([]store.MsgKey, len(entries))
	for i, e := range entries {
		out[i] = store.KeyOf(e.msg)
	}
	return out
}

func jitter(d time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d/2)+1))
	if err != nil {
		return d
	}
	return d/2 + time.Duration(n.Int64())
}

func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
