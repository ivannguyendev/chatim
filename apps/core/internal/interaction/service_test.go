package interaction_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.deps(t, nil)
	full.Now = nil
	for name, drop := range map[string]func(d *interaction.Deps){
		"no access":       func(d *interaction.Deps) { d.Access = nil },
		"no messages":     func(d *interaction.Deps) { d.Messages = nil },
		"no interactions": func(d *interaction.Deps) { d.Interactions = nil },
		"no counts":       func(d *interaction.Deps) { d.Counts = nil },
		"no count timers": func(d *interaction.Deps) { d.CountTimers = nil },
		"no pins":         func(d *interaction.Deps) { d.Pins = nil },
		"no projector":    func(d *interaction.Deps) { d.Projector = nil },
		"no events":       func(d *interaction.Deps) { d.Events = nil },
	} {
		d := full
		drop(&d)
		_, err := interaction.New(d)
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
		want := "invalid argument: interaction service needs access, messages, interactions, reaction counts, message count timers, pins, a pin projector and events"
		if err.Error() != want {
			t.Fatalf("%s: New = %q, want %q", name, err, want)
		}
	}
	if _, err := interaction.New(full); err != nil {
		t.Fatalf("New with a default clock: %v", err)
	}
}

func TestNewRefusesLimitsOutOfBoundsWithTheLimitsError(t *testing.T) {
	over := make([]string, interaction.MaxEmojiList+1)
	for i := range over {
		over[i] = "e" + strconv.Itoa(i)
	}
	cases := map[string]struct {
		limits interaction.Limits
		text   string
	}{
		"bad pin limit":     {interaction.Limits{PinLimit: interaction.MaxPinLimit + 1}, "invalid argument: pin limit 1001 must be 1 to 1000"},
		"negative pins":     {interaction.Limits{PinLimit: -1}, "invalid argument: pin limit -1 must be 1 to 1000"},
		"no emojis":         {interaction.Limits{Emojis: []string{}}, "invalid argument: need 1 to 100 reaction emojis, got 0"},
		"too many emojis":   {interaction.Limits{Emojis: over}, "invalid argument: need 1 to 100 reaction emojis, got 101"},
		"duplicate emoji":   {interaction.Limits{Emojis: []string{"👍", "❤️", "👍"}}, `invalid argument: reaction emoji "👍" listed twice`},
		"control character": {interaction.Limits{Emojis: []string{"a\u0007"}}, `reaction emoji "a\a": `},
	}
	rg := newRig(t, nil)
	for name, c := range cases {
		d := rg.deps(t, nil)
		d.Limits = c.limits
		_, err := interaction.New(d)
		want := c.limits.Validate()
		if err == nil || want == nil || err.Error() != want.Error() || !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want the Limits error %v", name, err, want)
		}
		if got := err.Error(); len(got) < len(c.text) || got[:len(c.text)] != c.text {
			t.Fatalf("%s: New = %q, want it to start with %q", name, got, c.text)
		}
	}
}
