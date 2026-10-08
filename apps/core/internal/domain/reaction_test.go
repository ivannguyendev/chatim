package domain_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestValidateEmoji(t *testing.T) {
	tests := []struct {
		name  string
		emoji string
		ok    bool
	}{
		{"thumbs up", "👍", true},
		{"skin tone", "👍🏽", true},
		{"variation selector", "❤️", true},
		{"zero width joiner family", "👨‍👩‍👧", true},
		{"looks like a field path", "$e", true},
		{"has a dot", "a.b", true},
		{"32 bytes", strings.Repeat("a", 32), true},
		{"empty", "", false},
		{"33 bytes", strings.Repeat("a", 33), false},
		{"invalid utf-8", "\xff", false},
		{"newline", "a\nb", false},
		{"nul", "\x00", false},
		{"delete", "\x7f", false},
		{"c1 control", "\u0085", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateEmoji(tt.emoji)
			if tt.ok {
				if err != nil {
					t.Fatalf("ValidateEmoji(%q) = %v, want nil", tt.emoji, err)
				}
				return
			}
			assertInvalid(t, err, "emoji")
		})
	}
}

func TestSortReactionCountsByCountThenEmoji(t *testing.T) {
	counts := []domain.ReactionCount{{Emoji: "😂", Count: 1}, {Emoji: "👍", Count: 3}, {Emoji: "$e", Count: 1}, {Emoji: "❤️", Count: 3}}
	domain.SortReactionCounts(counts)
	want := []domain.ReactionCount{{Emoji: "❤️", Count: 3}, {Emoji: "👍", Count: 3}, {Emoji: "$e", Count: 1}, {Emoji: "😂", Count: 1}}
	if !slices.Equal(counts, want) {
		t.Fatalf("sorted = %v, want %v", counts, want)
	}
}

func TestNewMessagesCarryNoReactions(t *testing.T) {
	m := domain.Message{Room: 1, Seq: 1, Text: "hi", CreatedAt: time.UnixMilli(1)}
	if m.Reactions.Version != 0 || m.Reactions.Counts != nil {
		t.Fatalf("new message carries reactions %+v", m.Reactions)
	}
	r := domain.Reaction{Room: 1, Seq: 1, User: "alice"}
	if r.Emoji != "" || r.Prev != "" || r.N != 0 {
		t.Fatalf("zero reaction = %+v, want a tombstone-shaped zero", r)
	}
}
