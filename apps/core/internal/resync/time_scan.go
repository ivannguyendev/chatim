package resync

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type timeScan[T any] struct {
	name    string
	limit   int
	full    error
	between func(ctx context.Context, room uint64, from, to time.Time, limit int) ([]T, error)
	record  func(T) work.Record
	counted *int
}

func scanByTime[T any](ctx context.Context, s *scanner, room uint64, ts timeScan[T]) error {
	from, edge := s.opts.From, map[string]bool{}
	for {
		page, err := ts.between(ctx, room, from, s.opts.To, ts.limit)
		if err != nil {
			return fmt.Errorf("%s of room %d from %s: %w", ts.name, room, from.Format(time.RFC3339Nano), err)
		}
		next, err := emitTimePage(ctx, s, ts, page, edge)
		if err != nil || len(page) < ts.limit {
			return err
		}
		last := ts.record(page[len(page)-1]).CommittedAt
		if !last.After(from) {
			return fmt.Errorf("%w: room %d at %s", ts.full, room, last.Format(time.RFC3339Nano))
		}
		from, edge = last, next
	}
}

func emitTimePage[T any](ctx context.Context, s *scanner, ts timeScan[T], page []T, edge map[string]bool) (map[string]bool, error) {
	next := map[string]bool{}
	if len(page) == 0 {
		return next, nil
	}
	end := ts.record(page[len(page)-1]).CommittedAt
	for _, item := range page {
		rec := ts.record(item)
		id := rec.ID()
		if rec.CommittedAt.Equal(end) {
			next[id] = true
		}
		if edge[id] {
			continue
		}
		if err := s.emit(ctx, rec); err != nil {
			return nil, err
		}
		*ts.counted++
	}
	return next, nil
}
