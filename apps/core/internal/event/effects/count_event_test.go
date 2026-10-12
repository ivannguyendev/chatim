package effects_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (rg *reactRig) count(t *testing.T, seq uint64, deltas ...store.EmojiDelta) domain.ReactionSummary {
	t.Helper()
	s, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: room, Seq: seq}, deltas)
	if err != nil {
		t.Fatalf("AddReactionCounts on seq %d: %v", seq, err)
	}
	return s
}

func plus(emoji string) store.EmojiDelta { return store.EmojiDelta{Emoji: emoji, Delta: 1} }

func TestCountEventDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).counts.Effect()
	if e.Name != effects.CountEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.CountEventName, delay)
	}
}

func TestCountEventRepublishesTheCurrentCountsOncePerMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.message(t, room, 2)
	rg.count(t, 1, plus("👍"))
	rg.count(t, 1, plus("❤️"))
	rg.count(t, 2, plus("🎉"))
	recs := []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 1), reactionRec(2, "bob", 1), reactionRec(1, "bob", 2)}
	if errs := rg.counts.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	ids := storedEventIDs(rg.js)
	if !slices.Equal(ids, []string{pbconv.ReactionCountsEventID(room, 0, 1, 2), pbconv.ReactionCountsEventID(room, 0, 2, 1)}) || len(rg.js.Attempts()) != 2 {
		t.Fatalf("stored %v after %d attempts, want one counts_changed per message at its current version", ids, len(rg.js.Attempts()))
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.message.4242.counts_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.CountsChanged(domain.RoomGroup, rg.stored(t, 1), countedAt); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if rg.counts.Republished() != 2 || rg.counts.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 2 and 0", rg.counts.Republished(), rg.counts.Dropped())
	}
}

func TestCountEventCountsOnlyRepublishesTheStreamKept(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.count(t, 1, plus("👍"))
	for range 2 {
		if errs := rg.counts.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if rg.counts.Republished() != 1 || len(rg.js.Attempts()) != 2 || len(rg.js.Stored()) != 1 {
		t.Fatalf("republished %d, attempts %d, stored %d; want 1, 2 and 1", rg.counts.Republished(), len(rg.js.Attempts()), len(rg.js.Stored()))
	}
}

func TestCountEventSkipsAMessageThatNeverHadCounts(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	if errs := rg.counts.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.counts.Dropped() != 0 {
		t.Fatalf("attempts %d, dropped %d; want nothing published for version 0", len(rg.js.Attempts()), rg.counts.Dropped())
	}
}

func TestCountEventDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, otherRoom, 1)
	if _, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: otherRoom, Seq: 1}, []store.EmojiDelta{plus("👍")}); err != nil {
		t.Fatalf("AddReactionCounts in a room without a doc: %v", err)
	}
	other := reactionRec(1, "bob", 1)
	other.Room = otherRoom
	recs := []work.Record{reactionRec(9, "bob", 1), reactionRec(9, "carol", 1), other}
	if errs := rg.counts.Effect().Run(t.Context(), recs); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if rg.counts.Dropped() != 3 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("dropped %d, attempts %d; want the missing message twice, the missing room once and nothing published", rg.counts.Dropped(), len(rg.js.Attempts()))
	}
}

func TestCountEventRetriesEveryRecordWhenTheStoreFails(t *testing.T) {
	broken, err := effects.NewCountEvent(
		effects.CountEventDeps{Messages: brokenStore{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewCountEvent: %v", err)
	}
	errs := broken.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 1)})
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || broken.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both records retried", errs, broken.Dropped())
	}
}
