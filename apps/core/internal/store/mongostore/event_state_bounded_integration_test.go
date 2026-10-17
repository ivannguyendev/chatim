package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var stateCollectionNames = []string{messagesCollection, editsCollection, interactionsCollection, pinActionsCollection, membersCollection, hiddenCollection, roomsCollection}

func TestPendingScansOnlyTheFlaggedDocsInStateOrder(t *testing.T) {
	s, db, finds, _ := monitoredStateStore(t)
	seedBoundedLookups(t, s)
	unflagAll(t, db)
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
	unflagAll(t, db)
	base := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	msgs := flaggedMessages()
	members, reactions := flaggedVersioned()
	all := slices.Concat(msgs, members, reactions)
	seedStates(t, db, all, base)
	expectPendingIndexKeys(t, db, len(msgs), messagesCollection)
	expectPendingIndexKeys(t, db, len(members), membersCollection)
	expectPendingIndexKeys(t, db, len(reactions), interactionsCollection)

	updates.reset()
	if err := s.EventStates().Clear(t.Context(), all); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	expectPendingIndexKeys(t, db, 0, messagesCollection, membersCollection, interactionsCollection)
	expectPointWrites(t, "Clear messages", explainEachUpdate(t, db, updates, messagesCollection), 4, len(msgs))
	expectPointWrites(t, "Clear members", explainEachUpdate(t, db, updates, membersCollection), len(members), len(members))
	expectPointWrites(t, "Clear reactions", explainEachUpdate(t, db, updates, interactionsCollection), len(reactions), len(reactions))

	if err := s.EventStates().Mark(t.Context(), store.StateMembers, members); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	expectPendingIndexKeys(t, db, len(members), membersCollection)
	expectPointWrites(t, "Mark members", explainEachUpdate(t, db, updates, membersCollection), len(members), len(members))

	if err := s.EventStates().Mark(t.Context(), store.StateMessages, msgs); err != nil {
		t.Fatalf("Mark messages: %v", err)
	}
	expectPendingIndexKeys(t, db, len(msgs), messagesCollection)
	expectPointWrites(t, "Mark messages", explainEachUpdate(t, db, updates, messagesCollection), 4, len(msgs))
}

func unflagAll(t *testing.T, db *mongo.Database) {
	t.Helper()
	unset := bson.D{{Key: "$unset", Value: bson.D{{Key: stateField, Value: ""}, {Key: stateAtField, Value: ""}}}}
	for _, coll := range stateCollectionNames {
		if _, err := db.Collection(coll).UpdateMany(t.Context(), bson.D{stateExists}, unset); err != nil {
			t.Fatalf("unflag %s: %v", coll, err)
		}
	}
}
