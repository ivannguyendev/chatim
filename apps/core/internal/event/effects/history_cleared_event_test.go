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
)

func (rg *memberRig) clear(t *testing.T, user string, at time.Time) time.Time {
	t.Helper()
	got, _, err := rg.rooms.ClearHistory(t.Context(), room, user, at)
	if err != nil {
		t.Fatalf("ClearHistory(%s): %v", user, err)
	}
	return got
}

func TestHistoryClearedEventRepublishesTheCurrentMark(t *testing.T) {
	rg := newMemberRig(t)
	if e := rg.cleared.Effect(); e.Name != effects.HistoryClearedEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.HistoryClearedEventName, delay)
	}
	rg.clear(t, "alice", memberNow.Add(-time.Hour))
	mark := rg.clear(t, "alice", memberNow.Add(-time.Minute))
	recs := []work.Record{clearedRec(room, "alice"), clearedRec(room, "alice")}
	if errs := rg.cleared.Effect().Run(t.Context(), recs); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ClearedEventID(room, "alice", mark)}) {
		t.Fatalf("stored = %v, want only the current mark", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.member.4242.history_cleared" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.HistoryCleared(rg.room, "alice", mark, memberNow); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if rg.cleared.Republished() != 1 || rg.cleared.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.cleared.Republished(), rg.cleared.Dropped())
	}
}

func TestHistoryClearedEventDropsWhatIsGoneAndRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	recs := []work.Record{clearedRec(room, "bob"), clearedRec(room, "carol")}
	if errs := rg.cleared.Effect().Run(t.Context(), recs); !allNil(errs, 2) || rg.cleared.Dropped() != 2 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("errs %v, dropped %d, attempts %d; want a never cleared member and a missing one dropped", errs, rg.cleared.Dropped(), len(rg.js.Attempts()))
	}
	eff := built(effects.NewHistoryClearedEvent(
		effects.MemberEventDeps{Members: brokenMembers{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	))(t)
	if errs := eff.Effect().Run(t.Context(), recs[:1]); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
