package memstore

import (
	"bytes"
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Mentions) List(ctx context.Context, q store.MentionQuery) ([]domain.Mention, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMentionQuery(q); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Mention{}
	for _, docs := range s.docs {
		for _, d := range docs {
			if d.Live && d.Tenant == q.Tenant && q.Wants(d) && mentionBefore(d, q.Before) {
				out = append(out, d)
			}
		}
	}
	slices.SortFunc(out, func(a, b domain.Mention) int { return compareMentionPlace(b, a) })
	return out[:min(len(out), q.Limit)], nil
}

func mentionBefore(d domain.Mention, c store.MentionCursor) bool {
	if c.At.IsZero() {
		return true
	}
	return cmp.Or(d.CreatedAt.Compare(c.At), bytes.Compare(mentionMsgKey(store.MsgKey(d.Key)), mentionMsgKey(c.Key))) < 0
}

func compareMentionPlace(a, b domain.Mention) int {
	return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), bytes.Compare(mentionMsgKey(store.MsgKey(a.Key)), mentionMsgKey(store.MsgKey(b.Key))))
}

func mentionMsgKey(k store.MsgKey) []byte { return keys.Msg(k.Room, k.Thread, k.Seq) }
