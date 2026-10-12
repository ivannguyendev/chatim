package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestHiddenEventRepublishesAHideWithItsTime(t *testing.T) {
	rg := newMemberRig(t)
	if e := rg.hide.Effect(); e.Name != effects.HiddenEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.HiddenEventName, delay)
	}
	hiddenAt := memberNow.Add(-time.Minute)
	if _, err := rg.hidden.Hide(t.Context(), "alice", store.MsgKey{Room: room, Thread: 9, Seq: 3}, hiddenAt); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if errs := rg.hide.Effect().Run(t.Context(), []work.Record{hiddenRec(room, "alice", 9, 3)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.HiddenEventID(room, "alice", 9, 3)}) {
		t.Fatalf("stored = %v", got)
	}
	events, err := rg.js.Events()
	if want := pbconv.MessageHidden(rg.room, "alice", 9, 3, hiddenAt); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if rg.hide.Republished() != 1 || rg.hide.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.hide.Republished(), rg.hide.Dropped())
	}
}

func TestHiddenEventDropsAMissingHideAndRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMemberRig(t)
	if _, err := rg.hidden.Hide(t.Context(), "alice", store.MsgKey{Room: otherRoom, Seq: 3}, memberNow); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	recs := []work.Record{hiddenRec(room, "alice", 0, 4), hiddenRec(otherRoom, "alice", 0, 3)}
	if errs := rg.hide.Effect().Run(t.Context(), recs); !allNil(errs, 2) || rg.hide.Dropped() != 2 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("errs %v, dropped %d, attempts %d; want no hide and no room dropped", errs, rg.hide.Dropped(), len(rg.js.Attempts()))
	}
	eff := built(effects.NewHiddenEvent(
		effects.HiddenEventDeps{Hidden: brokenMembers{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	))(t)
	if errs := eff.Effect().Run(t.Context(), recs[:1]); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
