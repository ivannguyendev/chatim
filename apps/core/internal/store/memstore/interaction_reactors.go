package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Interactions) Reactors(ctx context.Context, q store.ReactorQuery) ([]store.Reactor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateReactorQuery(q); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []store.Reactor{}
	for k, r := range s.reactions {
		x := store.Reactor{User: k.user, Emoji: r.Emoji, At: toMillis(r.At)}
		if k.key == q.Key && r.Emoji != "" && q.Wants(x) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, q.Order)
	return out[:min(len(out), q.Limit)], nil
}
