package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestReactionCounterDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).counter.Effect()
	if e.Name != effects.ReactionCounterName || e.Delay != countDelay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.ReactionCounterName, countDelay)
	}
}

func TestReactionCounterTouchesEachMessageOnceWithEveryWitness(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.message(t, room, 2)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "carol", "👍")
	rg.react(t, 1, "bob", "❤️")
	rg.react(t, 2, "bob", "🎉")
	recs := []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 1), reactionRec(2, "bob", 1), reactionRec(1, "bob", 2)}
	if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	want := []touchCall{
		{key: store.MsgKey{Room: room, Seq: 1}, witnesses: []store.Witness{{User: "bob", N: 2}, {User: "carol", N: 1}}, tries: 2},
		{key: store.MsgKey{Room: room, Seq: 2}, witnesses: []store.Witness{{User: "bob", N: 1}}, tries: 2},
	}
	if !slices.EqualFunc(rg.touches.calls, want, sameTouch) {
		t.Fatalf("touches = %+v, want one per message with the newest change of each user", rg.touches.calls)
	}
	counts := []domain.ReactionCount{{Emoji: "❤️", Count: 1}, {Emoji: "👍", Count: 1}}
	domain.SortReactionCounts(counts)
	if got := rg.stored(t, 1).Reactions; got.Version != 1 || !slices.Equal(got.Counts, counts) {
		t.Fatalf("summary of seq 1 = %+v, want %v at version 1", got, counts)
	}
	ids := storedEventIDs(rg.js)
	if !slices.Equal(ids, []string{pbconv.ReactionCountsEventID(room, 0, 1, 1), pbconv.ReactionCountsEventID(room, 0, 2, 1)}) {
		t.Fatalf("stored = %v", ids)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.message.4242.counts_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.CountsChanged(domain.RoomGroup, rg.stored(t, 1), countedAt); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if rg.counter.Repaired() != 2 || rg.counter.Republished() != 2 || rg.counter.Dropped() != 0 {
		t.Fatalf("repaired %d, republished %d, dropped %d; want 2, 2 and 0", rg.counter.Repaired(), rg.counter.Republished(), rg.counter.Dropped())
	}
}

func TestReactionCounterRepublishesAnUpToDateSummaryWithoutRepairing(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	recs := []work.Record{reactionRec(1, "bob", 1)}
	for range 2 {
		if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if rg.counter.Repaired() != 1 || rg.counter.Republished() != 1 || len(rg.js.Attempts()) != 2 || len(rg.js.Stored()) != 1 {
		t.Fatalf("repaired %d, republished %d, attempts %d, stored %d; want 1, 1, 2 and 1",
			rg.counter.Repaired(), rg.counter.Republished(), len(rg.js.Attempts()), len(rg.js.Stored()))
	}
	if got := rg.stored(t, 1).Reactions; got.Version != 1 {
		t.Fatalf("summary = %+v, want version 1 kept by the equal recount", got)
	}
}

func TestReactionCounterSkipsAMessageThatNeverHadCounts(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "bob", "")
	if errs := rg.counter.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.touches.calls) != 1 || rg.counter.Repaired() != 0 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("touches %d, repaired %d, attempts %d; want 1, 0 and 0", len(rg.touches.calls), rg.counter.Repaired(), len(rg.js.Attempts()))
	}
}

func TestReactionCounterRetriesEveryRecordOfAStaleMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.message(t, room, 2)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 2, "bob", "🎉")
	errs := rg.counter.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 4), reactionRec(2, "bob", 1)})
	if len(errs) != 3 || !errors.Is(errs[0], store.ErrStaleRead) || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want a stale read for both records of seq 1 only", errs)
	}
	if ids := storedEventIDs(rg.js); !slices.Equal(ids, []string{pbconv.ReactionCountsEventID(room, 0, 2, 1)}) || rg.counter.Dropped() != 0 {
		t.Fatalf("stored %v, dropped %d; want only seq 2 counted", ids, rg.counter.Dropped())
	}
}

func TestReactionCounterDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, 999, 1)
	other := work.Record{Kind: store.ReactionChanged, Room: 999, Seq: 1, Version: 1, User: "bob", CommittedAt: time.Now()}
	recs := []work.Record{reactionRec(9, "bob", 1), reactionRec(9, "carol", 1), other}
	if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if rg.counter.Dropped() != 3 || len(rg.touches.calls) != 0 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("dropped %d, touches %d, attempts %d; want 3, 0 and 0", rg.counter.Dropped(), len(rg.touches.calls), len(rg.js.Attempts()))
	}
}

func TestReactionCounterRetriesWhenTheStoreFails(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.touches.err = errBoom
	errs := rg.counter.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 1)})
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || rg.counter.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both records retried", errs, rg.counter.Dropped())
	}
	broken, err := effects.NewReactionCounter(
		effects.ReactionCounterDeps{Messages: brokenStore{}, Counter: &spyCounter{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.ReactionCounterConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewReactionCounter: %v", err)
	}
	if errs := broken.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want a retryable failure", errs)
	}
}
