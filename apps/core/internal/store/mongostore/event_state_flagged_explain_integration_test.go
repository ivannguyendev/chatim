package mongostore

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestPendingUsesTheStateIndexOnDocsFlaggedByWrites(t *testing.T) {
	s, db, finds := monitoredStore(t)
	seedBoundedLookups(t, s)
	all := pendingIndexKeys(t, db, messagesCollection)
	if all != len(boundedRooms)*(boundedMain+boundedThread) {
		t.Fatalf("%s keys after inserts = %d, want every inserted message", statePendingIndex, all)
	}
	var published []store.StateTarget
	for seq := uint64(1); seq <= boundedMain; seq++ {
		published = append(published, store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: boundedRoomA, Seq: seq})
	}
	if err := s.EventStates().Clear(t.Context(), published); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	expectPendingIndexKeys(t, db, all-boundedMain, messagesCollection)

	before := time.Now()
	finds.reset()
	docs, next, err := s.EventStates().Pending(t.Context(), store.StateMessages, time.Time{}, before, store.PendingCursor{}, 4)
	if err != nil || len(docs) != 4 || next.IsZero() {
		t.Fatalf("Pending page 1 = %d docs, next %+v, %v; want 4 and a cursor", len(docs), next, err)
	}
	expectPendingScan(t, "Pending page 1 on write flags", explainLast(t, db, finds, messagesCollection), 4)

	finds.reset()
	docs, next, err = s.EventStates().Pending(t.Context(), store.StateMessages, time.Time{}, before, next, 4)
	if err != nil || len(docs) != 4 || next.IsZero() {
		t.Fatalf("Pending page 2 = %d docs, next %+v, %v; want 4 and a cursor", len(docs), next, err)
	}
	expectPendingScan(t, "Pending page 2 on write flags", explainLast(t, db, finds, messagesCollection), 4)
	for _, d := range docs {
		if d.Room == boundedRoomA && d.Thread == 0 {
			t.Fatalf("Pending returned cleared message %+v", d)
		}
	}
}
