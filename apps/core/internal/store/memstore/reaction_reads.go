package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Reactions) Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range witnesses {
		if cur, ok := s.docs[reactionKey{key: key, user: w.User}]; !ok || cur.N < w.N {
			return nil, fmt.Errorf("witness %q at change %d on %+v: %w", w.User, w.N, key, store.ErrStaleRead)
		}
	}
	by := map[string]uint32{}
	for k, r := range s.docs {
		if k.key == key && r.Emoji != "" {
			by[r.Emoji]++
		}
	}
	out := make([]domain.ReactionCount, 0, len(by))
	for emoji, n := range by {
		out = append(out, domain.ReactionCount{Emoji: emoji, Count: n})
	}
	domain.SortReactionCounts(out)
	return out, nil
}

func (s *Reactions) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxReactionScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Reaction{}
	for _, r := range s.docs {
		if r.Room == room && !r.At.Before(from) && !r.At.After(to) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, reactionOrder)
	return out[:min(len(out), limit)], nil
}

func reactionOrder(a, b domain.Reaction) int {
	return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.User, b.User))
}
