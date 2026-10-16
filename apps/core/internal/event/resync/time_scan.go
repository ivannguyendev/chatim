package resync

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type timeScan[T any] struct {
	name    string
	limit   int
	full    error
	between func(ctx context.Context, room uint64, from, to time.Time, limit int) ([]T, error)
	at      func(T) time.Time
	marks   func(T) []mark
}

func scanByTime[T any](ctx context.Context, s *scanner, room uint64, ts timeScan[T]) error {
	from, edge := s.opts.From, map[store.StateTarget]bool{}
	for {
		page, err := ts.between(ctx, room, from, s.opts.To, ts.limit)
		if err != nil {
			return fmt.Errorf("%s of room %d from %s: %w", ts.name, room, from.Format(time.RFC3339Nano), err)
		}
		next, err := markTimePage(ctx, s, ts, page, edge)
		if err != nil || len(page) < ts.limit {
			return err
		}
		last := ts.at(page[len(page)-1])
		if !last.After(from) {
			return fmt.Errorf("%w: room %d at %s", ts.full, room, last.Format(time.RFC3339Nano))
		}
		from, edge = last, next
	}
}

func markTimePage[T any](ctx context.Context, s *scanner, ts timeScan[T], page []T, edge map[store.StateTarget]bool) (map[store.StateTarget]bool, error) {
	next := map[store.StateTarget]bool{}
	if len(page) == 0 {
		return next, nil
	}
	end := ts.at(page[len(page)-1])
	for _, item := range page {
		marks := ts.marks(item)
		if len(marks) == 0 {
			continue
		}
		id := marks[0].target
		if ts.at(item).Equal(end) {
			next[id] = true
		}
		if edge[id] {
			continue
		}
		for _, m := range marks {
			if err := s.mark(ctx, m); err != nil {
				return nil, err
			}
		}
	}
	return next, nil
}
