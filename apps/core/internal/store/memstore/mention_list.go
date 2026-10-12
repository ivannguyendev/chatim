package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Mentions) ListByTarget(ctx context.Context, q store.TargetMentionQuery) ([]domain.Mention, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateTargetMentionQuery(q); err != nil {
		return nil, err
	}
	return s.listWhere(q.Limit, func(d domain.Mention) bool {
		return d.Live && d.Tenant == q.Tenant && d.Target.Name(d.Key.Room) == q.Target && (!q.UnreadOnly || !d.Read) && q.Before.Holds(d)
	}), nil
}

func (s *Mentions) ListShared(ctx context.Context, q store.SharedMentionQuery) ([]domain.Mention, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateSharedMentionQuery(q); err != nil {
		return nil, err
	}
	return s.listWhere(q.Limit, q.Wants), nil
}

func (s *Mentions) listWhere(limit int, keep func(domain.Mention) bool) []domain.Mention {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Mention{}
	for _, docs := range s.docs {
		for _, d := range docs {
			if keep(d) {
				out = append(out, d)
			}
		}
	}
	slices.SortFunc(out, func(a, b domain.Mention) int { return store.CompareMentions(b, a) })
	return out[:min(len(out), limit)]
}
