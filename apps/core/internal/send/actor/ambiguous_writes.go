package actor

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type uncertainWrite struct {
	msg      domain.Message
	reserved bool
	until    time.Time
}

func (a *actor) findOnce(ctx context.Context, keys []store.MsgKey) ([]domain.Message, error) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reservationMargin)
	defer cancel()
	return a.r.msgs.Find(fctx, a.id, keys)
}

func (a *actor) remember(e *entry) {
	now := time.Now()
	for k, u := range a.uncertain {
		if !now.Before(u.until) {
			delete(a.uncertain, k)
		}
	}
	a.uncertain[e.key] = uncertainWrite{msg: e.msg, reserved: e.reserved, until: now.Add(a.r.cfg.ReservationTTL)}
}

func (a *actor) recheck(ctx context.Context, k dedupeKey) bool {
	u, ok := a.uncertain[k]
	if !ok {
		return false
	}
	if !time.Now().Before(u.until) {
		delete(a.uncertain, k)
		return false
	}
	key := store.KeyOf(u.msg)
	found, err := a.findOnce(ctx, []store.MsgKey{key})
	if err != nil {
		a.r.log.WarnContext(ctx, "find of an uncertain message write failed", "room", a.id, "seq", u.msg.Seq, "err", err)
		a.cache.fail(k, errCIDUnsettled)
		return true
	}
	for _, doc := range found {
		if store.KeyOf(doc) != key {
			continue
		}
		delete(a.uncertain, k)
		if doc.From == u.msg.From && doc.CID == u.msg.CID {
			a.landLate(ctx, k, doc)
		} else {
			a.releaseLost(ctx, k, u)
		}
		return true
	}
	a.cache.fail(k, errCIDUnsettled)
	return true
}

func (a *actor) landLate(ctx context.Context, k dedupeKey, doc domain.Message) {
	a.last = max(a.last, doc.Seq)
	ack := ackOf(doc)
	a.cache.commit(k, ack)
	_ = a.r.cids.Commit(context.WithoutCancel(ctx), []dedupe.Entry{{Key: a.remoteKey(k), Record: dedupe.Record(ack)}})
}

func (a *actor) releaseLost(ctx context.Context, k dedupeKey, u uncertainWrite) {
	a.dirty = true
	if u.reserved {
		_ = a.r.cids.Abort(context.WithoutCancel(ctx), []dedupe.Key{a.remoteKey(k)})
	}
	a.cache.fail(k, errCIDUnsettled)
}
