package store

import (
	"context"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type EmojiDelta struct {
	Emoji string
	Delta int
}

type MessageCounts interface {
	ReactionSummaries
	AddReactionCounts(ctx context.Context, key MsgKey, deltas []EmojiDelta) (domain.ReactionSummary, error)
	AddReplyCount(ctx context.Context, key MsgKey, delta int) (domain.ReplyCount, error)
	SetReplyCount(ctx context.Context, key MsgKey, base uint64, n uint32) (bool, error)
}

func ValidateEmojiDeltas(deltas []EmojiDelta) error {
	if len(deltas) == 0 {
		return invalid("emoji deltas")
	}
	seen := make(map[string]struct{}, len(deltas))
	for _, d := range deltas {
		if err := domain.ValidateEmoji(d.Emoji); err != nil {
			return err
		}
		if err := ValidateCountDelta(d.Delta); err != nil {
			return err
		}
		if _, dup := seen[d.Emoji]; dup {
			return invalid("duplicate emoji delta")
		}
		seen[d.Emoji] = struct{}{}
	}
	return nil
}

func ValidateCountDelta(delta int) error {
	if delta == 0 || delta > math.MaxUint32 || delta < -math.MaxUint32 {
		return invalid("count delta")
	}
	return nil
}

func AddEmojiDeltas(cur []domain.ReactionCount, deltas []EmojiDelta) []domain.ReactionCount {
	sums := make(map[string]int64, len(cur)+len(deltas))
	for _, c := range cur {
		sums[c.Emoji] = int64(c.Count)
	}
	for _, d := range deltas {
		sums[d.Emoji] += int64(d.Delta)
	}
	out := make([]domain.ReactionCount, 0, len(sums))
	for e, n := range sums {
		if n > 0 {
			out = append(out, domain.ReactionCount{Emoji: e, Count: uint32(min(n, math.MaxUint32))})
		}
	}
	domain.SortReactionCounts(out)
	return out
}
