package resync

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const markPage = 200

type mark struct {
	target  store.StateTarget
	counted *int
}

func (s *scanner) mark(ctx context.Context, m mark) error {
	if s.opts.DryRun {
		*m.counted++
		return nil
	}
	if n := len(s.batch); n > 0 && (n == markPage || s.batch[0].target.Collection != m.target.Collection) {
		if err := s.flush(ctx); err != nil {
			return err
		}
	}
	if err := s.pace(ctx); err != nil {
		return err
	}
	s.batch = append(s.batch, m)
	return nil
}

func (s *scanner) flush(ctx context.Context) error {
	if len(s.batch) == 0 {
		return nil
	}
	c := s.batch[0].target.Collection
	targets := make([]store.StateTarget, len(s.batch))
	for i, m := range s.batch {
		targets[i] = m.target
	}
	if err := s.deps.States.Mark(ctx, c, targets); err != nil {
		return fmt.Errorf("mark %d %s docs of room %d: %w", len(targets), c, targets[0].Room, err)
	}
	for _, m := range s.batch {
		*m.counted++
	}
	s.batch = s.batch[:0]
	return nil
}
