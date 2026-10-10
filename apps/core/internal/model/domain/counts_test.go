package domain_test

import (
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestSettleReactionsHidesEntriesAtOrBelowZero(t *testing.T) {
	raw := []domain.RawCount{{Emoji: "❤️", N: 1}, {Emoji: "😂", N: 0}, {Emoji: "👍", N: 2}, {Emoji: "😮", N: -1}, {Emoji: "🎉", N: math.MaxUint32 + 5}}
	got := domain.SettleReactions(raw, 7)
	want := []domain.ReactionCount{{Emoji: "🎉", Count: math.MaxUint32}, {Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}
	if !slices.Equal(got.Counts, want) || got.Version != 7 || !got.Unsettled {
		t.Fatalf("SettleReactions = %+v, want %v at version 7 marked unsettled", got, want)
	}
	clean := domain.SettleReactions([]domain.RawCount{{Emoji: "👍", N: 1}}, 1)
	if clean.Unsettled || len(clean.Counts) != 1 {
		t.Fatalf("SettleReactions of positive counts = %+v, want settled", clean)
	}
	if empty := domain.SettleReactions(nil, 0); empty.Unsettled || len(empty.Counts) != 0 {
		t.Fatalf("SettleReactions(nil) = %+v, want nothing", empty)
	}
}

func TestSettleRepliesClampsBelowZero(t *testing.T) {
	cases := map[int64]domain.ReplyCount{
		3:  {N: 3, Version: 2},
		0:  {N: 0, Version: 2},
		-1: {N: 0, Version: 2, Unsettled: true},
	}
	for n, want := range cases {
		if got := domain.SettleReplies(n, 2); got != want {
			t.Errorf("SettleReplies(%d) = %+v, want %+v", n, got, want)
		}
	}
}

func TestRawCountsKeepsEveryEntry(t *testing.T) {
	got := domain.RawCounts([]domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}})
	if !slices.Equal(got, []domain.RawCount{{Emoji: "👍", N: 2}, {Emoji: "❤️", N: 1}}) {
		t.Fatalf("RawCounts = %v", got)
	}
}
