package timer_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestACorrectMessageCountIsOnlyAnnouncedAgain(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1})
	before := rg.stored(t, 1)
	for _, c := range []timer.Check{messageCheck(room, 1, pbconv.ReactionsCounter), messageCheck(room, 2, pbconv.RepliesCounter)} {
		if err := rg.repair.Repair(t.Context(), c); err != nil {
			t.Fatalf("Repair(%+v): %v", c, err)
		}
	}
	if after := rg.stored(t, 1); after.Reactions.Version != before.Reactions.Version || len(rg.timers.all()) != 0 || rg.repair.Repaired(pbconv.ReactionsCounter) != 0 {
		t.Fatalf("ver %d -> %d, armed %v; want nothing written", before.Reactions.Version, after.Reactions.Version, rg.timers.all())
	}
	if got := rg.events.ids(); !slices.Equal(got, []string{pbconv.ReactionCountsEventID(room, 0, 1, 1)}) {
		t.Fatalf("events %v; want only the counted message announced again", got)
	}
}

func TestADriftedReactionCountIsRewrittenAfterAFollowUpTimer(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "carol", "❤️")
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1})
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1})
	if err := rg.repair.Repair(t.Context(), messageCheck(room, 1, pbconv.ReactionsCounter)); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	want := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "❤️", Count: 1}, {Emoji: "👍", Count: 1}}, Version: 3}
	got := rg.stored(t, 1)
	if got.Reactions.Version != want.Version || !slices.Equal(got.Reactions.Counts, want.Counts) || rg.repair.Repaired(pbconv.ReactionsCounter) != 1 {
		t.Fatalf("stored %+v, repaired %d; want %+v once", got.Reactions, rg.repair.Repaired(pbconv.ReactionsCounter), want)
	}
	if a := rg.timers.all(); !slices.Equal(a, []armed{{room: room, key: store.MsgKey{Room: room, Seq: 1}, counter: pbconv.ReactionsCounter}}) {
		t.Fatalf("armed %v, want one follow-up timer for the reactions of seq 1", a)
	}
	if len(rg.events.events) != 1 || !proto.Equal(rg.events.events[0], pbconv.CountsChanged(domain.RoomGroup, got, repairedAt)) {
		t.Fatalf("events = %v; want the repaired summary announced once", rg.events.events)
	}
}

func TestADriftedReplyCountIsArmedThenRewrittenAndAnnounced(t *testing.T) {
	rg := newCountRig(t)
	rg.reply(t, 1, 2)
	rg.reply(t, 1, 3)
	var calls []string
	rg.timers.calls = &calls
	d := rg.deps()
	d.Counts = orderedCounts{MessageCountWriter: rg.msgs, calls: &calls}
	rg.repair = rg.build(t, d)
	if err := rg.repair.Repair(t.Context(), messageCheck(room, 1, pbconv.RepliesCounter)); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	got := rg.stored(t, 1)
	if got.Replies != (domain.ReplyCount{N: 2, Version: 1}) || rg.repair.Repaired(pbconv.RepliesCounter) != 1 || !slices.Equal(calls, []string{"arm", "set"}) {
		t.Fatalf("stored %+v, repaired %d, calls %v; want {2 1} written after the timer", got.Replies, rg.repair.Repaired(pbconv.RepliesCounter), calls)
	}
	want := pbconv.ReplyCountsChanged(domain.RoomGroup, got, repairedAt)
	if len(rg.events.events) != 1 || !proto.Equal(rg.events.events[0], want) || want.GetId() != pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 1) {
		t.Fatalf("events = %v; want replies v1 announced", rg.events.events)
	}
}

func TestAMessageCountMovedDuringTheRepairFailsWithoutWriting(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	d := rg.deps()
	d.Interactions = movingInteractions{MessageCountReader: rg.inter, move: func() { rg.addReactions(t, 1, store.EmojiDelta{Emoji: "😮", Delta: 1}) }}
	rg.repair = rg.build(t, d)
	if err := rg.repair.Repair(t.Context(), messageCheck(room, 1, pbconv.ReactionsCounter)); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("Repair = %v, want a retry after the CAS missed", err)
	}
	if got := rg.stored(t, 1); got.Reactions.Version != 1 || !slices.Equal(got.Reactions.Counts, []domain.ReactionCount{{Emoji: "😮", Count: 1}}) {
		t.Fatalf("stored %+v, want only the concurrent change", got.Reactions)
	}
	if len(rg.events.ids()) != 0 || rg.repair.Repaired(pbconv.ReactionsCounter) != 0 || len(rg.timers.all()) != 1 {
		t.Fatalf("events %v, armed %v; want no event and the follow-up timer left armed", rg.events.ids(), rg.timers.all())
	}
}

func TestAFailedMessageArmWritesNothing(t *testing.T) {
	rg := newCountRig(t)
	rg.reply(t, 1, 2)
	rg.timers.err = errBoom
	if err := rg.repair.Repair(t.Context(), messageCheck(room, 1, pbconv.RepliesCounter)); !errors.Is(err, errBoom) {
		t.Fatalf("Repair = %v, want the arm error", err)
	}
	if got := rg.stored(t, 1); got.Replies != (domain.ReplyCount{}) || len(rg.events.ids()) != 0 {
		t.Fatalf("stored %+v, events %v; want nothing written or sent", got.Replies, rg.events.ids())
	}
}

func TestARepairCleansStoredCountsAtOrBelowZero(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1}, store.EmojiDelta{Emoji: "😮", Delta: -1})
	if _, err := rg.msgs.AddReplyCount(t.Context(), store.MsgKey{Room: room, Seq: 2}, -1); err != nil {
		t.Fatalf("AddReplyCount: %v", err)
	}
	for _, c := range []timer.Check{messageCheck(room, 1, pbconv.ReactionsCounter), messageCheck(room, 2, pbconv.RepliesCounter)} {
		if err := rg.repair.Repair(t.Context(), c); err != nil {
			t.Fatalf("Repair(%+v): %v", c, err)
		}
	}
	if got := rg.stored(t, 1).Reactions; got.Unsettled || got.Version != 2 || !slices.Equal(got.Counts, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("reactions after = %+v, want the recount written at version 2", got)
	}
	if got := rg.stored(t, 2).Replies; got != (domain.ReplyCount{N: 0, Version: 2}) {
		t.Fatalf("replies after = %+v, want 0 rewritten at version 2", got)
	}
}

func TestCountRepairReportsGoneTargetsAndStoreErrors(t *testing.T) {
	rg := newCountRig(t)
	if err := rg.repair.Repair(t.Context(), messageCheck(room, 9, pbconv.ReactionsCounter)); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Repair of a missing message = %v", err)
	}
	if err := rg.repair.Repair(t.Context(), messageCheck(otherRoom, 1, pbconv.RepliesCounter)); !errors.Is(err, domain.ErrMessageNotFound) && !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Repair in a missing room = %v", err)
	}
	d := rg.deps()
	d.Interactions = brokenCounts{}
	if err := rg.build(t, d).Repair(t.Context(), messageCheck(room, 1, pbconv.RepliesCounter)); !errors.Is(err, errBoom) {
		t.Fatalf("Repair with the store down = %v", err)
	}
	if n := rg.repair.Repaired(timer.MembersCounter); n != 0 {
		t.Fatalf("Repaired(members) = %d, want 0", n)
	}
}

func TestNewCountRepairNeedsEveryDependency(t *testing.T) {
	rg := newCountRig(t)
	strip := map[string]func(*timer.CountRepairDeps){
		"messages":     func(d *timer.CountRepairDeps) { d.Messages = nil },
		"interactions": func(d *timer.CountRepairDeps) { d.Interactions = nil },
		"counts":       func(d *timer.CountRepairDeps) { d.Counts = nil },
		"timers":       func(d *timer.CountRepairDeps) { d.Timers = nil },
		"rooms":        func(d *timer.CountRepairDeps) { d.Rooms = nil },
		"events":       func(d *timer.CountRepairDeps) { d.Events = nil },
	}
	for name, cut := range strip {
		d := rg.deps()
		cut(&d)
		if _, err := timer.NewCountRepair(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("without %s: err = %v", name, err)
		}
	}
}
