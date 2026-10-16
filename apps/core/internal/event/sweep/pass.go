package sweep

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	passFailedMsg = "event sweep of a collection failed; the next pass tries again"
	staleMsg      = "pending state older than the sweep checkpoint"
)

func (s *Sweeper) pass(ctx context.Context, l *lead) error {
	for _, c := range store.AllStateCollections() {
		if _, ok := s.deps.Rebuilders[c]; !ok {
			continue
		}
		err := s.sweep(ctx, l, c)
		switch {
		case errors.Is(err, errLostLead) || errors.Is(err, errStopped) || ctx.Err() != nil:
			return err
		case err != nil:
			s.fails.Warn(ctx, passFailedMsg, "collection", c.String(), "err", err)
		}
	}
	s.stats.passes.Add(1)
	return nil
}

func (s *Sweeper) sweep(ctx context.Context, l *lead, c store.StateCollection) error {
	checkpoint, err := s.deps.Checkpoints.Get(ctx, c)
	if err != nil {
		return err
	}
	now := time.Now()
	before := now.Add(-s.cfg.MinAge)
	from, stale := s.window(checkpoint, l.fresh[c])
	var failedAt time.Time
	var after store.PendingCursor
	for first := true; ; first = false {
		docs, next, err := s.deps.States.Pending(ctx, c, from, before, after, s.cfg.Batch)
		if err != nil {
			return err
		}
		if first {
			s.stats.setOldest(c, now, docs)
		}
		s.warnStale(ctx, c, docs, stale)
		pageFailed, err := s.page(ctx, c, docs)
		if err != nil {
			return err
		}
		failedAt = earliest(failedAt, pageFailed)
		switch {
		case !s.leading():
			return errLostLead
		case next.IsZero():
			return s.advance(ctx, l, c, now, checkpoint, before, failedAt)
		case s.stopping():
			return errStopped
		}
		after = next
	}
}

func (s *Sweeper) window(checkpoint time.Time, fresh bool) (from, stale time.Time) {
	if checkpoint.IsZero() {
		return time.Time{}, time.Time{}
	}
	start := checkpoint.Add(-s.cfg.Lookback)
	if fresh {
		return time.Time{}, start
	}
	return start, time.Time{}
}

func (s *Sweeper) advance(ctx context.Context, l *lead, c store.StateCollection, now, checkpoint, before, failedAt time.Time) error {
	mark := before
	if !failedAt.IsZero() {
		mark = failedAt
	}
	if err := s.deps.Checkpoints.Advance(ctx, c, mark); err != nil {
		return err
	}
	return s.probe(ctx, l, c, now, later(checkpoint, mark).Add(-s.cfg.Lookback))
}

func (s *Sweeper) probe(ctx context.Context, l *lead, c store.StateCollection, now, windowStart time.Time) error {
	docs, _, err := s.deps.States.Pending(ctx, c, time.Time{}, windowStart, store.PendingCursor{}, 1)
	if err != nil {
		return err
	}
	l.fresh[c] = len(docs) > 0
	s.stats.raiseOldest(c, now, docs)
	s.warnStale(ctx, c, docs, windowStart)
	return nil
}

func (s *Sweeper) warnStale(ctx context.Context, c store.StateCollection, docs []store.PendingDoc, stale time.Time) {
	if stale.IsZero() {
		return
	}
	for _, d := range docs {
		if d.StateAt.Before(stale) {
			s.stale.Warn(ctx, staleMsg, "collection", c.String(), "room", d.Room, "state_at", d.StateAt, "window_start", stale)
			return
		}
	}
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
