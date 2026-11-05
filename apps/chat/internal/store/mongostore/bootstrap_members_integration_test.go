package mongostore

import (
	"errors"
	"testing"
)

func TestBootstrapClustersMembers(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, membersCollection)
	assertMemberIndexes(t, db)
	if got := indexKeys(t, db.Collection(membersCollection)); hasIndex(got, "room_id:1,user_id:1", true) {
		t.Fatalf("members indexes = %v, want no unique room_id:1,user_id:1", got)
	}
}

func TestBootstrapRejectsUnclusteredMembers(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := db.CreateCollection(t.Context(), membersCollection); err != nil {
		t.Fatalf("create plain members: %v", err)
	}
	if err := Bootstrap(t.Context(), db); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("Bootstrap error = %v, want ErrNotClustered", err)
	}
}
