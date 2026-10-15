package mongostore

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var stateCollectionNames = []string{messagesCollection, editsCollection, interactionsCollection, pinActionsCollection, membersCollection, hiddenCollection, roomsCollection}

func TestPendingScansOnlyTheFlaggedDocsInStateOrder(t *testing.T) {
	s, db, finds, _ := monitoredStateStore(t)
	seedBoundedLookups(t, s)
	expectPendingIndexKeys(t, db, 0, stateCollectionNames...)
	msgs := flaggedMessages()
	base := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	seedStates(t, db, msgs[:6], base)
	seedStates(t, db, msgs[6:], base.Add(time.Second))
	expectPendingIndexKeys(t, db, len(msgs), messagesCollection)

	finds.reset()
	docs, next, err := s.EventStates().Pending(t.Context(), store.StateMessages, base, base.Add(time.Minute), store.PendingCursor{}, 4)
	if err != nil || len(docs) != 4 || next.IsZero() {
		t.Fatalf("Pending page 1 = %d docs, next %+v, %v; want 4 and a cursor", len(docs), next, err)
	}
	expectPendingScan(t, "Pending page 1", explainLast(t, db, finds, messagesCollection), 4)

	finds.reset()
	docs, next, err = s.EventStates().Pending(t.Context(), store.StateMessages, base, base.Add(time.Minute), next, 4)
	if err != nil || len(docs) != 4 || next.IsZero() || !docs[2].StateAt.Equal(base.Add(time.Second)) {
		t.Fatalf("Pending page 2 = %+v, next %+v, %v; want 4 docs crossing the time tie and a cursor", docs, next, err)
	}
	expectPendingScan(t, "Pending page 2", explainLast(t, db, finds, messagesCollection), 4)
}

func TestClearAndMarkStayBoundedOnClusteredKeys(t *testing.T) {
	s, db, _, updates := monitoredStateStore(t)
	seedBoundedLookups(t, s)
	base := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	msgs := flaggedMessages()
	members, reactions := flaggedVersioned()
	all := slices.Concat(msgs, members, reactions)
	seedStates(t, db, all, base)
	expectPendingIndexKeys(t, db, len(msgs), messagesCollection)
	expectPendingIndexKeys(t, db, len(members), membersCollection)
	expectPendingIndexKeys(t, db, len(reactions), interactionsCollection)

	if err := s.EventStates().Clear(t.Context(), all); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	expectPendingIndexKeys(t, db, 0, messagesCollection, membersCollection, interactionsCollection)
	expectBoundedUpdate(t, "Clear messages", explainLastUpdate(t, db, updates, messagesCollection), len(msgs))
	expectBoundedUpdate(t, "Clear members", explainLastUpdate(t, db, updates, membersCollection), len(members))
	expectBoundedUpdate(t, "Clear reactions", explainLastUpdate(t, db, updates, interactionsCollection), len(reactions))

	if err := s.EventStates().Mark(t.Context(), store.StateMembers, members); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	expectPendingIndexKeys(t, db, len(members), membersCollection)
	expectBoundedUpdate(t, "Mark members", explainLastUpdate(t, db, updates, membersCollection), len(members))
}

func expectBoundedUpdate(t *testing.T, name string, c lookupCost, keys int) {
	t.Helper()
	if c.examined > 2*keys+1 || c.examined < keys || strings.Contains(c.plan, "COLLSCAN") {
		t.Errorf("%s: examined %d docs for %d keys, plan %s; want %d..%d and no COLLSCAN", name, c.examined, keys, c.plan, keys, 2*keys+1)
	}
}
