package store_test

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestValidateEmojiDeltas(t *testing.T) {
	ok := [][]store.EmojiDelta{
		{{Emoji: "👍", Delta: 1}},
		{{Emoji: "👍", Delta: -1}, {Emoji: "❤️", Delta: 1}},
	}
	for _, d := range ok {
		if err := store.ValidateEmojiDeltas(d); err != nil {
			t.Errorf("ValidateEmojiDeltas(%v) = %v, want nil", d, err)
		}
	}
	bad := map[string][]store.EmojiDelta{
		"none":       nil,
		"zero delta": {{Emoji: "👍", Delta: 0}},
		"no emoji":   {{Emoji: "", Delta: 1}},
		"duplicate":  {{Emoji: "👍", Delta: 1}, {Emoji: "👍", Delta: -1}},
		"huge delta": {{Emoji: "👍", Delta: math.MaxUint32 + 1}},
	}
	for name, d := range bad {
		if err := store.ValidateEmojiDeltas(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateEmojiDeltas = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestValidateCountDelta(t *testing.T) {
	for _, d := range []int{1, -1, math.MaxUint32, -math.MaxUint32} {
		if err := store.ValidateCountDelta(d); err != nil {
			t.Errorf("ValidateCountDelta(%d) = %v, want nil", d, err)
		}
	}
	for _, d := range []int{0, math.MaxUint32 + 1, -math.MaxUint32 - 1} {
		if err := store.ValidateCountDelta(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("ValidateCountDelta(%d) = %v, want ErrInvalidArgument", d, err)
		}
	}
}

func TestAddEmojiDeltasFoldsCountsAndDropsEmptyOnes(t *testing.T) {
	cur := []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}
	got := store.AddEmojiDeltas(cur, []store.EmojiDelta{{Emoji: "❤️", Delta: -1}, {Emoji: "😂", Delta: 1}, {Emoji: "👍", Delta: 1}})
	want := []domain.ReactionCount{{Emoji: "👍", Count: 3}, {Emoji: "😂", Count: 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("AddEmojiDeltas = %v, want %v", got, want)
	}
	if got := store.AddEmojiDeltas(nil, []store.EmojiDelta{{Emoji: "👍", Delta: -1}}); len(got) != 0 {
		t.Fatalf("AddEmojiDeltas below zero = %v, want nothing", got)
	}
	if !slices.Equal(cur, []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}) {
		t.Fatalf("AddEmojiDeltas changed its input to %v", cur)
	}
}
