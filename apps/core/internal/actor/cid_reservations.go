package actor

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type landing struct {
	e   *entry
	msg domain.Message
	ack Ack
}

type failure struct {
	e       *entry
	err     error
	release bool
}

func (a *actor) remoteKey(k dedupeKey) dedupe.Key {
	return dedupe.Key{Room: a.id, User: k.user, CID: k.cid}
}

func (a *actor) reserve(ctx context.Context, fresh []*entry) []*entry {
	if len(fresh) == 0 {
		return fresh
	}
	now := time.Now()
	keys := make([]dedupe.Key, len(fresh))
	for i, e := range fresh {
		e.admittedAt = now
		keys[i] = a.remoteKey(e.key)
	}
	verdicts, err := a.r.cids.Reserve(ctx, keys)
	if err != nil || len(verdicts) != len(fresh) {
		return fresh
	}
	kept := fresh[:0]
	for i, e := range fresh {
		switch v := verdicts[i]; v.Status {
		case dedupe.Committed:
			a.cache.commit(e.key, Ack(v.Record))
		case dedupe.PendingElsewhere:
			a.cache.fail(e.key, errCIDElsewhere)
		case dedupe.PendingHere:
			a.cache.fail(e.key, errCIDUnsettled)
		default:
			e.reserved = v.Status == dedupe.Reserved
			kept = append(kept, e)
		}
	}
	return kept
}

func (a *actor) conclude(ctx context.Context) {
	if len(a.landed) == 0 && len(a.failed) == 0 {
		return
	}
	detached := context.WithoutCancel(ctx)
	if len(a.landed) > 0 {
		entries := make([]dedupe.Entry, len(a.landed))
		for i, l := range a.landed {
			entries[i] = dedupe.Entry{Key: a.remoteKey(l.e.key), Record: dedupe.Record(l.ack)}
		}
		_ = a.r.cids.Commit(detached, entries)
	}
	var released []dedupe.Key
	for _, f := range a.failed {
		if f.release {
			released = append(released, a.remoteKey(f.e.key))
		}
	}
	if len(released) > 0 {
		_ = a.r.cids.Abort(detached, released)
	}
	for _, l := range a.landed {
		a.cache.commit(l.e.key, l.ack)
	}
	a.publishLanded()
	for _, f := range a.failed {
		a.cache.fail(f.e.key, f.err)
	}
	clear(a.landed)
	clear(a.failed)
	a.landed, a.failed = a.landed[:0], a.failed[:0]
}
