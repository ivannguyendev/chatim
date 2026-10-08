package mutate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type touchCall struct {
	key       store.MsgKey
	cur       domain.ReactionSummary
	witnesses []store.Witness
	tries     int
}

type scriptedCounter struct {
	inner mutate.CounterToucher
	err   error
	calls []touchCall
}

func (c *scriptedCounter) Touch(ctx context.Context, k store.MsgKey, cur domain.ReactionSummary, ws []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	c.calls = append(c.calls, touchCall{key: k, cur: cur, witnesses: slices.Clone(ws), tries: tries})
	if c.err != nil {
		return domain.ReactionSummary{}, false, c.err
	}
	return c.inner.Touch(ctx, k, cur, ws, tries)
}

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
}

func TestReactTouchesTheCounterWithItsOwnWrite(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	spy := &scriptedCounter{inner: d.Counter}
	d.Counter = spy
	rg.m = rg.build(t, d)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	rg.mustReact(t, react("bob", 1, "❤️"))
	rg.mustReact(t, react("bob", 1, "❤️"))
	if len(spy.calls) != 2 {
		t.Fatalf("touched %d times, want 2 (none for the no-op)", len(spy.calls))
	}
	last := spy.calls[1]
	if last.key != key(1) || last.tries != mutate.FastTouchTries || !slices.Equal(last.witnesses, []store.Witness{{User: "bob", N: 2}}) ||
		!sameSummary(last.cur, counts(1, domain.ReactionCount{Emoji: "👍", Count: 1})) {
		t.Fatalf("touch = %+v, want seq 1, bob's change 2 as the witness, %d tries and the summary read before the write", last, mutate.FastTouchTries)
	}
}

func TestATouchFailureDoesNotFailTheReaction(t *testing.T) {
	for _, cause := range []error{store.ErrStaleRead, counter.ErrContended, errBoom} {
		rg := newRig(t, nil)
		d := rg.deps(t, nil)
		d.Counter = &scriptedCounter{err: cause}
		rg.m = rg.build(t, d)
		rg.send(t, 1, "alice", "hi")
		got := rg.mustReact(t, react("bob", 1, "👍"))
		if got.Change != 1 || !sameSummary(got.Reactions, domain.ReactionSummary{}) {
			t.Fatalf("%v: React = %+v, want the write acked with the summary it read", cause, got)
		}
		if s := rg.stored(t, 1); s.Reactions.Version != 0 {
			t.Fatalf("%v: summary %+v written without a touch", cause, s.Reactions)
		}
		if _, events := rg.events.list(); len(events) != 1 || events[0].GetReactionChanged() == nil {
			t.Fatalf("%v: events = %v, want only reaction_changed", cause, events)
		}
	}
	refused := newRig(t, nil)
	refused.events.err = errBoom
	refused.send(t, 1, "alice", "hi")
	if got := refused.mustReact(t, react("bob", 1, "👍")); got.Change != 1 || got.Reactions.Version != 1 {
		t.Fatalf("React with refused events = %+v, want change 1 counted", got)
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
