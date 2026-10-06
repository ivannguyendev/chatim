package reconcile_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func (rg *rig) react(t *testing.T, r, seq uint64, user, emoji string) {
	t.Helper()
	x := domain.Reaction{Room: r, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: time.Now().UTC()}
	if _, _, err := rg.reactions.Set(t.Context(), x); err != nil {
		t.Fatalf("react %d/%d by %s: %v", r, seq, user, err)
	}
}

func (rg *rig) pin(t *testing.T, r, pv, seq uint64) {
	t.Helper()
	a := domain.PinAction{Room: r, PV: pv, Tenant: tenant, Op: domain.PinOpPin, Seq: seq, By: "alice", At: time.Now().UTC()}
	if err := rg.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("pin %d v%d: %v", r, pv, err)
	}
}

func TestForwardsReactionChangesAndPinFactsAsRecords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.react(t, room, 1, "alice", "👍")
		rg.pin(t, room, 1, 1)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"x:4242-0-1-alice-n1", "p:4242-p1"}) {
			t.Fatalf("stored = %v, want the reaction and the pin record", got)
		}
		stored := rg.js.Stored()
		reaction, err := work.Decode(stored[0].Data)
		if err != nil || reaction.Kind != store.ReactionChanged || reaction.Room != room || reaction.Seq != 1 || reaction.Version != 1 || reaction.User != "alice" || !reaction.CommittedAt.Equal(committed) {
			t.Fatalf("reaction record = %+v, %v; want %d/0/1 n1 by alice at %v", reaction, err, room, committed)
		}
		fact, err := work.Decode(stored[1].Data)
		if err != nil || fact.Kind != store.PinInserted || fact.Room != room || fact.Seq != 1 || fact.User != "" || !fact.CommittedAt.Equal(committed) {
			t.Fatalf("pin record = %+v, %v; want room %d pv 1 at %v", fact, err, room, committed)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0", got)
		}
	})
}
