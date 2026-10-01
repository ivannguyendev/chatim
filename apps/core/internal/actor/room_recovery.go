package actor

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	maxRecoverPages = 10
	holeClockSkew   = time.Second
)

func (r *Router) Recover(ctx context.Context, room, from uint64) error {
	if room == 0 {
		return fmt.Errorf("%w: room", apperr.ErrInvalidArgument)
	}
	_, err := r.await(ctx, newRecovery(room, from))
	return err
}

func (c Config) holeGrace() time.Duration { return c.ReservationTTL + holeClockSkew }

func (a *actor) replay(ctx context.Context) {
	waiting := a.replays
	a.replays = nil
	from := waiting[0].from
	for _, q := range waiting[1:] {
		from = min(from, q.from)
	}
	err := a.republish(ctx, from)
	for _, q := range waiting {
		q.answer(Ack{}, err)
	}
}

func (a *actor) republish(ctx context.Context, from uint64) error {
	rctx, cancel := context.WithTimeout(ctx, a.r.cfg.GroupDeadline)
	defer cancel()
	a.stale = true
	if err := a.refresh(rctx); err != nil {
		a.r.log.WarnContext(ctx, "reload room timeline for recovery failed", "room", a.id, "err", err)
		return errUnavailable
	}
	w := &walk{a: a, next: from, until: a.last, left: maxRecoverPages * store.MaxPageLimit, now: time.Now()}
	for range maxRecoverPages {
		if !w.open() {
			break
		}
		page, err := a.r.msgs.Page(rctx, store.PageQuery{Room: a.id, Anchor: store.After, Seq: w.next, Limit: store.MaxPageLimit})
		if err != nil {
			a.r.log.WarnContext(ctx, "read room timeline for recovery failed", "room", a.id, "after", w.next, "err", err)
			return errUnavailable
		}
		w.read(page)
		if len(page) < store.MaxPageLimit {
			w.stuck = true
		}
	}
	return w.flush()
}

type walk struct {
	a      *actor
	next   uint64
	until  uint64
	left   int
	now    time.Time
	stuck  bool
	events []*chatimv1.Event
	skips  []uint64
	err    error
}

func (w *walk) open() bool {
	return w.err == nil && !w.stuck && w.left > 0 && w.next < w.until
}

func (w *walk) read(page []domain.Message) {
	for _, m := range page {
		if !w.open() || m.Pts > w.until {
			w.stuck = true
			return
		}
		if m.Pts <= w.next {
			continue
		}
		if m.Pts > w.next+1 && w.now.Sub(m.CreatedAt) < w.a.r.cfg.holeGrace() {
			w.stuck = true
			return
		}
		for w.next+1 < m.Pts && w.left > 0 {
			w.skip(w.next + 1)
		}
		if w.left == 0 {
			w.stuck = true
			return
		}
		w.event(m)
	}
}

func (w *walk) skip(pts uint64) {
	if len(w.events) > 0 {
		_ = w.flush()
	}
	w.skips = append(w.skips, pts)
	w.next, w.left = pts, w.left-1
}

func (w *walk) event(m domain.Message) {
	if len(w.skips) > 0 {
		_ = w.flush()
	}
	w.events = append(w.events, pbconv.MessageCreated(w.a.room.Type, m))
	w.next, w.left = m.Pts, w.left-1
}

func (w *walk) flush() error {
	if w.err == nil {
		switch {
		case len(w.events) > 0:
			w.err = w.a.r.events.Enqueue(w.a.id, w.events)
		case len(w.skips) > 0:
			w.a.r.log.Warn("recovery skips pts missing from the timeline", "room", w.a.id, "first", w.skips[0], "last", w.skips[len(w.skips)-1])
			w.err = w.a.r.events.Skip(w.a.id, w.skips)
		}
	}
	w.events, w.skips = nil, nil
	return w.err
}
