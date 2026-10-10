package mutate_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func react(user string, seq uint64, emoji string) mutate.ReactCmd {
	return mutate.ReactCmd{Tenant: tenant, User: user, Room: room, Seq: seq, Emoji: emoji}
}

func counts(v uint64, cs ...domain.ReactionCount) domain.ReactionSummary {
	return domain.ReactionSummary{Counts: cs, Version: v}
}

func sameSummary(a, b domain.ReactionSummary) bool {
	return a.Version == b.Version && slices.Equal(a.Counts, b.Counts)
}

func (rg *rig) reaction(t *testing.T, seq uint64, user string) (domain.Reaction, bool) {
	t.Helper()
	doc, found, err := rg.reactions.GetReaction(t.Context(), key(seq), user)
	if err != nil {
		t.Fatalf("reaction of %s on seq %d: %v", user, seq, err)
	}
	return doc, found
}

func (rg *rig) assertCounted(t *testing.T, calls []string, deltas ...store.EmojiDelta) {
	t.Helper()
	if got := rg.reactCalls.list(); !slices.Equal(got, calls) {
		t.Fatalf("calls = %v, want %v", got, calls)
	}
	if n := rg.msgTimers.pending(); n != 0 {
		t.Fatalf("%d count check timers left armed, want none", n)
	}
	got := rg.counts.list()
	if len(deltas) == 0 {
		if len(got) != 0 {
			t.Fatalf("counted %+v, want nothing", got)
		}
		return
	}
	if len(got) == 0 || got[len(got)-1].key != key(1) || !slices.Equal(got[len(got)-1].deltas, deltas) {
		t.Fatalf("counted %+v, want %v on seq 1 last", got, deltas)
	}
}

func (rg *rig) mustReact(t *testing.T, c mutate.ReactCmd) mutate.ReactResult {
	t.Helper()
	got, err := rg.m.React(t.Context(), c)
	if err != nil {
		t.Fatalf("%s reacts %q on seq %d: %v", c.User, c.Emoji, c.Seq, err)
	}
	return got
}

var counted = []string{"arm", "write", "count", "disarm"}

func up(emoji string) store.EmojiDelta { return store.EmojiDelta{Emoji: emoji, Delta: 1} }

func down(emoji string) store.EmojiDelta { return store.EmojiDelta{Emoji: emoji, Delta: -1} }

func TestReactWritesTheEmojiAndCountsItBeforeTheAck(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	got := rg.mustReact(t, react("bob", 1, "👍"))
	want := counts(1, domain.ReactionCount{Emoji: "👍", Count: 1})
	if got.Change != 1 || !sameSummary(got.Reactions, want) {
		t.Fatalf("React = %+v, want change 1 with %+v", got, want)
	}
	rg.assertCounted(t, counted, up("👍"))
	if c := rg.msgTimers.counters; !slices.Equal(c, []string{pbconv.ReactionsCounter}) {
		t.Fatalf("armed counters %v, want one reactions timer", c)
	}
	stored := rg.stored(t, 1)
	if !sameSummary(stored.Reactions, want) {
		t.Fatalf("stored summary = %+v, want %+v written by the inline $inc", stored.Reactions, want)
	}
	doc, _ := rg.reaction(t, 1, "bob")
	if doc.Emoji != "👍" || doc.Prev != "" || doc.N != 1 || doc.Tenant != tenant || !doc.At.Equal(rg.at()) {
		t.Fatalf("reaction = %+v, want 👍 as change 1 at %v", doc, rg.at())
	}
	rooms, events := rg.events.list()
	changed, count := pbconv.ReactionChanged(domain.RoomGroup, doc), pbconv.CountsChanged(domain.RoomGroup, stored, rg.at())
	if !slices.Equal(rooms, []uint64{room, room}) || len(events) != 2 || !proto.Equal(events[0], changed) || !proto.Equal(events[1], count) {
		t.Fatalf("enqueued %v %v, want reaction_changed then counts_changed", rooms, events)
	}
	if events[0].GetId() != pbconv.ReactionEventID(room, 0, 1, "bob", 1) || events[1].GetId() != pbconv.ReactionCountsEventID(room, 0, 1, 1) {
		t.Fatalf("event ids = %q, %q", events[0].GetId(), events[1].GetId())
	}
}

func TestReactWithTheSameEmojiChangesNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	first := rg.mustReact(t, react("bob", 1, "👍"))
	at := rg.at()
	rg.now = rg.now.Add(time.Second)
	again := rg.mustReact(t, react("bob", 1, "👍"))
	if again.Change != 1 || !sameSummary(again.Reactions, first.Reactions) {
		t.Fatalf("again = %+v, want %+v", again, first)
	}
	rg.assertCounted(t, append(slices.Clone(counted), "arm", "write", "disarm"), up("👍"))
	if len(rg.counts.list()) != 1 {
		t.Fatalf("counted %+v, want only the first reaction", rg.counts.list())
	}
	if doc, _ := rg.reaction(t, 1, "bob"); doc.N != 1 || !doc.At.Equal(at) {
		t.Fatalf("reaction = %+v, want the first write kept", doc)
	}
	if _, events := rg.events.list(); len(events) != 2 {
		t.Fatalf("a no-op enqueued events: %d in total, want the first 2", len(events))
	}
}

func TestReactReplacesTheEmojiOfTheUser(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	got := rg.mustReact(t, react("bob", 1, "❤️"))
	want := counts(2, domain.ReactionCount{Emoji: "❤️", Count: 1})
	if got.Change != 2 || !sameSummary(got.Reactions, want) {
		t.Fatalf("React = %+v, want change 2 with %+v", got, want)
	}
	rg.assertCounted(t, append(slices.Clone(counted), counted...), down("👍"), up("❤️"))
	doc, _ := rg.reaction(t, 1, "bob")
	if doc.Emoji != "❤️" || doc.Prev != "👍" || doc.N != 2 {
		t.Fatalf("reaction = %+v, want ❤️ replacing 👍 as change 2", doc)
	}
	_, events := rg.events.list()
	if len(events) != 4 || !proto.Equal(events[2], pbconv.ReactionChanged(domain.RoomGroup, doc)) || events[3].GetId() != pbconv.ReactionCountsEventID(room, 0, 1, 2) {
		t.Fatalf("events = %v, want reaction_changed n2 then counts_changed v2", events)
	}
}

func TestRemovingAReactionLeavesATombstone(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	got := rg.mustReact(t, react("bob", 1, ""))
	if got.Change != 2 || got.Reactions.Version != 2 || len(got.Reactions.Counts) != 0 {
		t.Fatalf("remove = %+v, want change 2 with no counts at version 2", got)
	}
	rg.assertCounted(t, append(slices.Clone(counted), counted...), down("👍"))
	if doc, found := rg.reaction(t, 1, "bob"); !found || doc.Emoji != "" || doc.Prev != "👍" || doc.N != 2 {
		t.Fatalf("reaction = %+v (found %v), want a tombstone after 👍 as change 2", doc, found)
	}
	_, events := rg.events.list()
	if len(events) != 4 || events[2].GetReactionChanged().GetEmoji() != "" || events[2].GetReactionChanged().GetPreviousEmoji() != "👍" {
		t.Fatalf("events = %v, want a removal event after 👍", events)
	}
	none := rg.mustReact(t, react("carol", 1, ""))
	if none.Change != 0 || none.Reactions.Version != 2 {
		t.Fatalf("remove without a reaction = %+v, want change 0 and the current summary", none)
	}
	if n := len(rg.counts.list()); n != 2 || rg.msgTimers.pending() != 0 {
		t.Fatalf("removing nothing counted (%d counts) or left %d timers armed", n, rg.msgTimers.pending())
	}
	if _, found := rg.reaction(t, 1, "carol"); found {
		t.Fatalf("removing nothing wrote a tombstone")
	}
	if _, events := rg.events.list(); len(events) != 4 {
		t.Fatalf("removing nothing enqueued events: %d in total, want 4", len(events))
	}
}

func TestADeletedMessageTakesOnlyRemovals(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, c := range []mutate.ReactCmd{react("bob", 1, "❤️"), react("carol", 1, "👍")} {
		if _, err := rg.m.React(t.Context(), c); !errors.Is(err, domain.ErrMessageDeleted) || !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("%s reacts %q on a deleted message = %v, want ErrMessageDeleted", c.User, c.Emoji, err)
		}
	}
	if got := rg.mustReact(t, react("bob", 1, "")); got.Change != 2 {
		t.Fatalf("removal on a deleted message = %+v, want change 2", got)
	}
	rg.assertCounted(t, append(slices.Clone(counted), counted...), down("👍"))
}
