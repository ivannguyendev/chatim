package mutate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestReactAcceptsOnlyTheConfiguredEmojis(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{Emojis: []string{"👍", "🎉"}}
	rg.m = rg.build(t, d)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.React(t.Context(), react("bob", 1, "❤️")); !errors.Is(err, domain.ErrEmojiNotAllowed) || !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("unlisted emoji = %v, want ErrEmojiNotAllowed", err)
	}
	if _, err := rg.m.React(t.Context(), react("mallory", 9, "❤️")); !errors.Is(err, domain.ErrEmojiNotAllowed) {
		t.Fatalf("unlisted emoji from a stranger on an unknown message = %v, want ErrEmojiNotAllowed before any read", err)
	}
	if _, found := rg.reaction(t, 1, "bob"); found {
		t.Fatalf("an unlisted emoji was written")
	}
	rg.mustReact(t, react("bob", 1, "🎉"))
	rg.mustReact(t, react("carol", 1, "👍"))
	if got := rg.mustReact(t, react("bob", 1, "")); got.Change != 2 || !sameSummary(got.Reactions, counts(3, domain.ReactionCount{Emoji: "👍", Count: 1})) {
		t.Fatalf("removal = %+v, want change 2 leaving 👍 once at version 3", got)
	}
}

func TestReactRejectsBadInputBeforeWriting(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	threaded := react("bob", 1, "👍")
	threaded.Thread = 1
	cases := []struct {
		name string
		cmd  mutate.ReactCmd
		want error
	}{
		{"control character", react("bob", 1, "\u0007"), apperr.ErrInvalidArgument},
		{"longer than 32 bytes", react("bob", 1, strings.Repeat("a", 33)), apperr.ErrInvalidArgument},
		{"not utf-8", react("bob", 1, "\xff"), apperr.ErrInvalidArgument},
		{"thread", threaded, apperr.ErrInvalidArgument},
		{"zero seq", react("bob", 0, "👍"), apperr.ErrInvalidArgument},
		{"unlisted emoji", react("bob", 1, "🎉"), domain.ErrEmojiNotAllowed},
		{"unknown message", react("bob", 9, "👍"), domain.ErrMessageNotFound},
		{"stranger", react("mallory", 1, "👍"), domain.ErrNotMember},
	}
	for _, c := range cases {
		if _, err := rg.m.React(t.Context(), c.cmd); !errors.Is(err, c.want) {
			t.Fatalf("%s: React = %v, want %v", c.name, err, c.want)
		}
	}
	if _, found := rg.reaction(t, 1, "bob"); found {
		t.Fatalf("a refused reaction was written")
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("refused reactions enqueued %v", events)
	}
	if calls := rg.reactCalls.list(); len(calls) != 0 {
		t.Fatalf("refused reactions reached %v", calls)
	}
}

func TestReactRefusesWithoutWritingWhenTheTimerIsNotArmed(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.msgTimers.err = errBoom
	if _, err := rg.m.React(t.Context(), react("bob", 1, "👍")); !errors.Is(err, domain.ErrRetryLater) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("React without a timer = %v, want ErrRetryLater (UNAVAILABLE)", err)
	}
	if calls := rg.reactCalls.list(); !slices.Equal(calls, []string{"arm"}) {
		t.Fatalf("calls = %v, want only the failed arm", calls)
	}
	if _, found := rg.reaction(t, 1, "bob"); found || rg.stored(t, 1).Reactions.Version != 0 {
		t.Fatalf("a reaction or count was written without a timer")
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("enqueued %v without a timer", events)
	}
}

func TestACountFailureKeepsTheTimerAndStillAcksTheReaction(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("carol", 1, "👍"))
	rg.counts.err = errBoom
	got := rg.mustReact(t, react("bob", 1, "👍"))
	want := counts(1, domain.ReactionCount{Emoji: "👍", Count: 2})
	if got.Change != 1 || !sameSummary(got.Reactions, want) {
		t.Fatalf("React = %+v, want change 1 with %+v folded on the summary it read", got, want)
	}
	if calls := rg.reactCalls.list(); !slices.Equal(calls[4:], []string{"arm", "write", "count"}) || rg.msgTimers.pending() != 1 {
		t.Fatalf("calls = %v, pending %d; want the timer left armed after the failed count", calls, rg.msgTimers.pending())
	}
	if s := rg.stored(t, 1); s.Reactions.Version != 1 {
		t.Fatalf("summary %+v changed by a failed count", s.Reactions)
	}
	if _, events := rg.events.list(); len(events) != 3 || events[2].GetReactionChanged() == nil {
		t.Fatalf("events = %v, want only reaction_changed for bob", events)
	}
	refused := newRig(t, nil)
	refused.events.err = errBoom
	refused.send(t, 1, "alice", "hi")
	if got := refused.mustReact(t, react("bob", 1, "👍")); got.Change != 1 || got.Reactions.Version != 1 {
		t.Fatalf("React with refused events = %+v, want change 1 counted", got)
	}
}

func TestAFailedReactionWriteKeepsTheTimerForAnUnknownOutcome(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Interactions = loggedWrites{Interactions: rg.reactions, log: rg.reactCalls, err: errBoom}
	rg.m = rg.build(t, d)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.React(t.Context(), react("bob", 1, "👍")); !errors.Is(err, errBoom) {
		t.Fatalf("React = %v, want the store error", err)
	}
	if calls := rg.reactCalls.list(); !slices.Equal(calls, []string{"arm", "write"}) || rg.msgTimers.pending() != 1 {
		t.Fatalf("calls = %v, pending %d; want the timer kept to recount a write that may have landed", calls, rg.msgTimers.pending())
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("enqueued %v after a failed write", events)
	}
}

func TestReactAsksThePolicyButIgnoresLockedKinds(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.React(t.Context(), react("bob", 1, "👍")); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("React = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 || asked[0].Action != access.ReactMessage || asked[0].User != "bob" || asked[0].Author != "alice" || asked[0].Kind != domain.KindText {
		t.Fatalf("policy asked %+v, want react_message by bob on alice's text", asked)
	}
	locked := newRig(t, access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}})
	locked.send(t, 1, "alice", "hi")
	if got := locked.mustReact(t, react("bob", 1, "👍")); got.Change != 1 {
		t.Fatalf("React on a locked kind = %+v, want it allowed (D94)", got)
	}
}
