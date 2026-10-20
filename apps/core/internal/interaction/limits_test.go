package interaction_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestLimitsFillDefaultsAndCheckBounds(t *testing.T) {
	var full, over []string
	for i := range interaction.MaxEmojiList + 1 {
		over = append(over, "e"+strconv.Itoa(i))
	}
	full = over[:interaction.MaxEmojiList]
	cases := []struct {
		name   string
		limits interaction.Limits
		bad    string
	}{
		{"defaults", interaction.Limits{}, ""},
		{"at the caps", interaction.Limits{Emojis: full, PinLimit: interaction.MaxPinLimit}, ""},
		{"one of each", interaction.Limits{Emojis: []string{"🎉"}, PinLimit: 1}, ""},
		{"empty list", interaction.Limits{Emojis: []string{}}, "emoji"},
		{"over the list cap", interaction.Limits{Emojis: over}, "101"},
		{"invalid emoji", interaction.Limits{Emojis: []string{"👍", strings.Repeat("x", 33)}}, strings.Repeat("x", 33)},
		{"control character", interaction.Limits{Emojis: []string{"a\u0007"}}, `a\a`},
		{"duplicate", interaction.Limits{Emojis: []string{"👍", "❤️", "👍"}}, "👍"},
		{"pin limit over the cap", interaction.Limits{PinLimit: interaction.MaxPinLimit + 1}, "1001"},
		{"negative pin limit", interaction.Limits{PinLimit: -1}, "-1"},
	}
	for _, c := range cases {
		err := c.limits.Validate()
		switch {
		case c.bad == "" && err != nil:
			t.Fatalf("%s: Validate() = %v, want nil", c.name, err)
		case c.bad != "" && (!errors.Is(err, apperr.ErrInvalidArgument) || !strings.Contains(err.Error(), c.bad)):
			t.Fatalf("%s: Validate() = %v, want ErrInvalidArgument naming %q", c.name, err, c.bad)
		}
	}
}

func TestReactionEmojisListsTheConfiguredEmojisInOrder(t *testing.T) {
	rg := newRig(t, nil)
	defaults := []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}
	got := rg.m.ReactionEmojis()
	if !slices.Equal(got, defaults) || !slices.Equal(interaction.DefaultEmojis, defaults) {
		t.Fatalf("ReactionEmojis() = %q with DefaultEmojis %q, want %q", got, interaction.DefaultEmojis, defaults)
	}
	got[0] = "x"
	if again := rg.m.ReactionEmojis(); again[0] != "👍" {
		t.Fatalf("a caller changed the mutator's list: %q", again)
	}
	own := []string{"🎉", "👍"}
	d := rg.deps(t, nil)
	d.Limits = interaction.Limits{Emojis: own}
	m := rg.build(t, d)
	own[0] = "x"
	if got := m.ReactionEmojis(); !slices.Equal(got, []string{"🎉", "👍"}) {
		t.Fatalf("ReactionEmojis() = %q, want the configured list kept from New", got)
	}
}
