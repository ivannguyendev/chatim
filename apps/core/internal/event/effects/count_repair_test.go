package effects_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestCountRepairRunsAtOnce(t *testing.T) {
	if e := newCountRig(t).repair.Effect(); e.Name != effects.CountRepairName || e.Delay != 0 {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.CountRepairName)
	}
}

func TestACorrectMessageCountIsOnlyAnnouncedAgain(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1})
	before := rg.stored(t, 1)
	if errs := rg.run(t, countRec(room, 1, pbconv.ReactionsCounter, 7), countRec(room, 2, pbconv.RepliesCounter, 8)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	after := rg.stored(t, 1)
	if after.Reactions.Version != before.Reactions.Version || len(rg.timers.checks()) != 0 || rg.repair.Repaired(pbconv.ReactionsCounter) != 0 {
		t.Fatalf("ver %d -> %d, armed %v, repaired %d; want nothing written", before.Reactions.Version, after.Reactions.Version, rg.timers.checks(), rg.repair.Repaired(pbconv.ReactionsCounter))
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ReactionCountsEventID(room, 0, 1, 1)}) || rg.repair.Republished() != 1 {
		t.Fatalf("stored %v, republished %d; want only the counted message announced again", got, rg.repair.Republished())
	}
}

func TestADriftedReactionCountIsRewrittenAfterAFollowUpTimer(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "carol", "❤️")
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1})
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1})
	if errs := rg.run(t, countRec(room, 1, pbconv.ReactionsCounter, 7), countRec(room, 1, pbconv.ReactionsCounter, 9)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	want := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "❤️", Count: 1}, {Emoji: "👍", Count: 1}}, Version: 3}
	got := rg.stored(t, 1)
	if got.Reactions.Version != want.Version || !slices.Equal(got.Reactions.Counts, want.Counts) || rg.repair.Repaired(pbconv.ReactionsCounter) != 1 {
		t.Fatalf("stored %+v, repaired %d; want %+v once", got.Reactions, rg.repair.Repaired(pbconv.ReactionsCounter), want)
	}
	if armed := rg.timers.checks(); !slices.Equal(armed, []armedCheck{{key: store.MsgKey{Room: room, Seq: 1}, counter: pbconv.ReactionsCounter}}) {
		t.Fatalf("armed %v, want one follow-up timer for the reactions of seq 1", armed)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 1 || !proto.Equal(events[0], pbconv.CountsChanged(domain.RoomGroup, got, repairedAt)) {
		t.Fatalf("events = %v, %v; want the repaired summary announced once", events, err)
	}
}

func TestADriftedReplyCountIsRewrittenAndAnnounced(t *testing.T) {
	rg := newCountRig(t)
	rg.reply(t, 1, 2)
	rg.reply(t, 1, 3)
	var calls []string
	rg.timers.onArm = func() { calls = append(calls, "arm") }
	d := rg.deps()
	d.Counts = orderedCounts{MessageCountWriter: rg.msgs, calls: &calls}
	rg.repair = rg.build(t, d)
	if errs := rg.run(t, countRec(room, 1, pbconv.RepliesCounter, 7)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	got := rg.stored(t, 1)
	if got.Replies != (domain.ReplyCount{N: 2, Version: 1}) || rg.repair.Repaired(pbconv.RepliesCounter) != 1 || !slices.Equal(calls, []string{"arm", "set"}) {
		t.Fatalf("stored %+v, repaired %d, calls %v; want {2 1} written after the timer", got.Replies, rg.repair.Repaired(pbconv.RepliesCounter), calls)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 1 || !proto.Equal(events[0], pbconv.ReplyCountsChanged(domain.RoomGroup, got, repairedAt)) || events[0].GetId() != pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 1) {
		t.Fatalf("events = %v, %v; want replies v1 announced", events, err)
	}
}

func TestACountMovedDuringTheRepairNaksWithoutWriting(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	d := rg.deps()
	d.Interactions = movingInteractions{MessageCountReader: rg.inter, move: func() { rg.addReactions(t, 1, store.EmojiDelta{Emoji: "😮", Delta: 1}) }}
	rg.repair = rg.build(t, d)
	errs := rg.run(t, countRec(room, 1, pbconv.ReactionsCounter, 7))
	if len(errs) != 1 || !errors.Is(errs[0], domain.ErrRetryLater) {
		t.Fatalf("errs = %v, want a retry after the CAS missed", errs)
	}
	got := rg.stored(t, 1)
	if got.Reactions.Version != 1 || !slices.Equal(got.Reactions.Counts, []domain.ReactionCount{{Emoji: "😮", Count: 1}}) {
		t.Fatalf("stored %+v, want only the concurrent change", got.Reactions)
	}
	if len(rg.js.Attempts()) != 0 || rg.repair.Repaired(pbconv.ReactionsCounter) != 0 || len(rg.timers.checks()) != 1 {
		t.Fatalf("attempts %d, repaired %d, armed %v; want no event, no repair and the follow-up timer left armed", len(rg.js.Attempts()), rg.repair.Repaired(pbconv.ReactionsCounter), rg.timers.checks())
	}
}

func TestAFailedFollowUpTimerWritesNothing(t *testing.T) {
	rg := newCountRig(t)
	rg.reply(t, 1, 2)
	rg.timers.err = errBoom
	if errs := rg.run(t, countRec(room, 1, pbconv.RepliesCounter, 7)); len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want a retry", errs)
	}
	if got := rg.stored(t, 1); got.Replies != (domain.ReplyCount{}) || len(rg.js.Attempts()) != 0 {
		t.Fatalf("stored %+v, attempts %d; want nothing written or sent", got.Replies, len(rg.js.Attempts()))
	}
}

func TestCountRepairDropsGoneTargetsAndRetriesStoreErrors(t *testing.T) {
	rg := newCountRig(t)
	recs := []work.Record{countRec(room, 9, pbconv.ReactionsCounter, 1), countRec(otherRoom, 1, pbconv.RepliesCounter, 2)}
	if errs := rg.repair.Effect().Run(t.Context(), recs); !allNil(errs, 2) || rg.repair.Dropped() != 2 {
		t.Fatalf("errs %v, dropped %d; want both records dropped", errs, rg.repair.Dropped())
	}
	d := rg.deps()
	d.Interactions = brokenCounts{}
	broken := rg.build(t, d)
	if errs := broken.Effect().Run(t.Context(), []work.Record{countRec(room, 1, pbconv.RepliesCounter, 3)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || broken.Dropped() != 0 {
		t.Fatalf("errs %v, dropped %d; want a retryable failure", errs, broken.Dropped())
	}
	if n := rg.repair.Repaired("members"); n != 0 {
		t.Fatalf("Repaired(members) = %d, want 0", n)
	}
}

func TestNewCountRepairNeedsEveryDependency(t *testing.T) {
	rg := newCountRig(t)
	full := rg.deps()
	strip := map[string]func(*effects.CountRepairDeps){
		"messages":     func(d *effects.CountRepairDeps) { d.Messages = nil },
		"interactions": func(d *effects.CountRepairDeps) { d.Interactions = nil },
		"counts":       func(d *effects.CountRepairDeps) { d.Counts = nil },
		"timers":       func(d *effects.CountRepairDeps) { d.Timers = nil },
		"rooms":        func(d *effects.CountRepairDeps) { d.Rooms = nil },
		"jetstream":    func(d *effects.CountRepairDeps) { d.JS = nil },
	}
	for name, cut := range strip {
		d := full
		cut(&d)
		if _, err := effects.NewCountRepair(d, effects.CountRepairConfig{SubjectRoot: "evt"}); err == nil {
			t.Errorf("without %s: NewCountRepair succeeded", name)
		}
	}
	if _, err := effects.NewCountRepair(full, effects.CountRepairConfig{}); err == nil {
		t.Errorf("without a subject root: NewCountRepair succeeded")
	}
}
