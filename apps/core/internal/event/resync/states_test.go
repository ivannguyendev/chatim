package resync_test

import (
	"math/bits"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (w world) pendingBits(t *testing.T, room uint64) int {
	t.Helper()
	n := 0
	for _, c := range store.AllStateCollections() {
		docs, _, err := w.states.Pending(t.Context(), c, time.Time{}, lostTo.AddDate(100, 0, 0), store.PendingCursor{}, store.MaxPendingPage)
		if err != nil {
			t.Fatalf("Pending(%s): %v", c, err)
		}
		for _, d := range docs {
			if d.Room == room {
				n += bits.OnesCount32(uint32(d.State))
			}
		}
	}
	return n
}

func TestResyncMarksMatchTheVersionsOfTheStoredDocs(t *testing.T) {
	w := newWorld(t)
	w.messages(t, newRoom, 3, lostFrom.Add(11*time.Minute))
	w.edit(t, newRoom, 2, 1, lostFrom.Add(14*time.Minute))
	w.react(t, newRoom, 1, "bob", "👍", lostFrom.Add(15*time.Minute))
	w.react(t, newRoom, 1, "bob", "🎉", lostFrom.Add(16*time.Minute))
	w.bookmark(t, newRoom, 2, "carol", true, lostFrom.Add(17*time.Minute))
	w.pin(t, newRoom, 1, 3, lostFrom.Add(18*time.Minute))
	w.join(t, newRoom, lostFrom.Add(19*time.Minute), "bob", "carol")
	if _, raised, err := w.rooms.ClearHistory(t.Context(), newRoom, "carol", lostFrom.Add(20*time.Minute)); err != nil || !raised {
		t.Fatalf("ClearHistory = %v, %v; want raised", raised, err)
	}
	pin := domain.PreferencesChange{Pinned: new(true)}
	if _, changed, err := w.rooms.SetPreferences(t.Context(), newRoom, "bob", pin, lostFrom.Add(21*time.Minute)); err != nil || !changed {
		t.Fatalf("SetPreferences = %v, %v", changed, err)
	}
	w.hide(t, newRoom, 3, "bob", lostFrom.Add(22*time.Minute))
	w.rename(t, newRoom, lostFrom.Add(23*time.Minute))
	opts := resync.Options{From: lostFrom, To: lostTo, Room: newRoom, Rate: resync.MaxRate}
	first := w.spy()
	if _, err := resync.Run(t.Context(), w.deps(first), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	marked := first.marked()
	if err := w.states.Clear(t.Context(), marked); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	cleared := w.pendingBits(t, newRoom)
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if got := w.pendingBits(t, newRoom); got != cleared+len(marked) || len(marked) != 18 {
		t.Fatalf("pending bits of room %d = %d after resync (%d before, %d marks, report %+v); want every one of the 18 marks to flag its doc", newRoom, got, cleared, len(marked), rep)
	}
}
