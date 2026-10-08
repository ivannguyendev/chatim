package domain

import (
	"cmp"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxEmojiBytes = 32

type Reaction struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	Tenant string
	User   string
	Emoji  string
	Prev   string
	N      uint32
	At     time.Time
}

type ReactionCount struct {
	Emoji string
	Count uint32
}

type ReactionSummary struct {
	Counts  []ReactionCount
	Version uint64
}

func ValidateEmoji(emoji string) error {
	if emoji == "" || len(emoji) > maxEmojiBytes || !utf8.ValidString(emoji) {
		return invalid("emoji")
	}
	for _, r := range emoji {
		if unicode.IsControl(r) {
			return invalid("emoji")
		}
	}
	return nil
}

func SortReactionCounts(counts []ReactionCount) {
	slices.SortFunc(counts, func(a, b ReactionCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Emoji, b.Emoji))
	})
}
