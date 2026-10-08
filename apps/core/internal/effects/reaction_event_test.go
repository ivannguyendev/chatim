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
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestReactionEventDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).event.Effect()
	if e.Name != effects.ReactionEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.ReactionEventName, delay)
	}
}

func TestReactionEventPublishesTheCurrentChange(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ReactionEventID(room, 0, 1, "bob", 1)}) {
		t.Fatalf("stored = %v", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.message.4242.reaction_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.ReactionChanged(domain.RoomGroup, rg.current(t, 1, "bob")); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want the fast path event %v", events, err, want)
	}
	if rg.event.Republished() != 1 || rg.event.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.event.Republished(), rg.event.Dropped())
	}
}

func TestReactionEventSkipsOlderChangesAndRetriesNewerOnes(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "bob", "❤️")
	errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "bob", 3), reactionRec(1, "bob", 2)})
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the change ahead of the read retried", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ReactionEventID(room, 0, 1, "bob", 2)}) || rg.event.Dropped() != 0 {
		t.Fatalf("stored %v, dropped %d; want only change 2", got, rg.event.Dropped())
	}
}

func TestReactionEventCountsOnlyEventsTheStreamLacked(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	fast, err := publish.Message("evt", room, pbconv.ReactionChanged(domain.RoomGroup, rg.current(t, 1, "bob")))
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.event.Republished() != 0 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 0", len(rg.js.Stored()), len(rg.js.Attempts()), rg.event.Republished())
	}
}

func TestReactionEventDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	elsewhere := domain.Reaction{Room: 999, Seq: 1, Tenant: tenant, User: "bob", Emoji: "👍", At: time.Now().UTC().Truncate(time.Millisecond)}
	if _, _, err := rg.reactions.Set(t.Context(), elsewhere); err != nil {
		t.Fatalf("Set: %v", err)
	}
	other := work.Record{Kind: store.ReactionChanged, Room: 999, Seq: 1, Version: 1, User: "bob", CommittedAt: time.Now()}
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "carol", 1), other}); !allNil(errs, 2) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.event.Dropped() != 2 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 2 (no reaction, no room)", len(rg.js.Attempts()), rg.event.Dropped())
	}
}

func TestReactionEventRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewReactionEvent(
		effects.ReactionEventDeps{Reactions: brokenReactions{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewReactionEvent: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
