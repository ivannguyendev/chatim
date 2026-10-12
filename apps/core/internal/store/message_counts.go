package store

import (
	"cmp"
	"context"
	"math"
	"slices"

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
	return domain.SettleReactions(AddRawEmojiDeltas(domain.RawCounts(cur), deltas), 0).Counts
}

func AddRawEmojiDeltas(raw []domain.RawCount, deltas []EmojiDelta) []domain.RawCount {
	out := slices.Clone(raw)
	for _, d := range deltas {
		if i := slices.IndexFunc(out, func(c domain.RawCount) bool { return c.Emoji == d.Emoji }); i >= 0 {
			out[i].N += int64(d.Delta)
			continue
		}
		out = append(out, domain.RawCount{Emoji: d.Emoji, N: int64(d.Delta)})
	}
	slices.SortFunc(out, func(a, b domain.RawCount) int {
		return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.Emoji, b.Emoji))
	})
	return out
}
