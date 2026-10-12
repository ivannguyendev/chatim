package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Interactions) CountReactions(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	by := map[string]uint32{}
	for k, r := range s.reactions {
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

func (s *Interactions) Between(ctx context.Context, q store.InteractionScan) ([]store.Interaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateInteractionScan(q); err != nil {
		return nil, err
	}
	s.mu.RLock()
	all := s.allOfKind(q.Kind)
	s.mu.RUnlock()
	out := []store.Interaction{}
	for _, x := range all {
		if x.Key.Room == q.Room && !x.At.Before(q.From) && !x.At.After(q.To) && (q.After == nil || store.CompareInteractions(x, *q.After) > 0) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, store.CompareInteractions)
	return out[:min(len(out), q.Limit)], nil
}

func (s *Interactions) allOfKind(kind keys.InteractionKind) []store.Interaction {
	var out []store.Interaction
	switch kind {
	case keys.ReactionKind:
		for k, r := range s.reactions {
			out = append(out, store.Interaction{Kind: kind, Key: k.key, User: k.user, Ver: r.N, At: r.At})
		}
	case keys.BookmarkKind:
		for k, b := range s.bookmarks {
			out = append(out, store.Interaction{Kind: kind, Key: k.key, User: k.user, Ver: b.Ver, At: b.At})
		}
	case keys.ReplyKind:
		for k, r := range s.replies {
			out = append(out, store.Interaction{Kind: kind, Key: k.parent, User: r.From, Ver: r.Ver, At: r.At, Reply: k.reply})
		}
	}
	return out
}
