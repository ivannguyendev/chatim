package domain

import "math"

type RawCount struct {
	Emoji string
	N     int64
}

func SettleReactions(raw []RawCount, version uint64) ReactionSummary {
	out := ReactionSummary{Counts: make([]ReactionCount, 0, len(raw)), Version: version}
	for _, c := range raw {
		if c.N <= 0 {
			out.Unsettled = true
			continue
		}
		out.Counts = append(out.Counts, ReactionCount{Emoji: c.Emoji, Count: narrowCount(c.N)})
	}
	SortReactionCounts(out.Counts)
	return out
}

func SettleReplies(n int64, version uint64) ReplyCount {
	return ReplyCount{N: narrowCount(n), Version: version, Unsettled: n < 0}
}

func RawCounts(counts []ReactionCount) []RawCount {
	out := make([]RawCount, len(counts))
	for i, c := range counts {
		out[i] = RawCount{Emoji: c.Emoji, N: int64(c.Count)}
	}
	return out
}

func narrowCount(n int64) uint32 {
	return uint32(min(max(n, 0), math.MaxUint32))
}
