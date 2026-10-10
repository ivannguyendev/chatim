package memstore

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"time"

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

func (s *Interactions) Between(ctx context.Context, room uint64, kind keys.InteractionKind, from, to time.Time, limit int) ([]store.Interaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateInteractionKind(kind); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxInteractionScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	all := s.allOfKind(kind)
	s.mu.RUnlock()
	out := []store.Interaction{}
	for _, x := range all {
		if x.Key.Room == room && !x.At.Before(from) && !x.At.After(to) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b store.Interaction) int {
		return cmp.Or(a.At.Compare(b.At), compareID(interactionID(a), interactionID(b)))
	})
	return out[:min(len(out), limit)], nil
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

func interactionID(x store.Interaction) []byte {
	msg := keys.Msg(x.Key.Room, x.Key.Thread, x.Key.Seq)
	if x.Kind == keys.ReplyKind {
		return keys.InteractionReply(msg, x.Reply.Thread, x.Reply.Seq)
	}
	return keys.InteractionUser(msg, x.Kind, x.User)
}

func compareID(a, b []byte) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), bytes.Compare(a, b))
}
