package mongostore

import (
	"errors"
	"testing"
)

func TestBootstrapCreatesEditAndHiddenCollections(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, editsCollection)
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(hiddenCollection)); !hasIndex(got, "u:1,r:1,th:1,s:1", true) {
		t.Fatalf("hidden indexes = %v, want unique u:1,r:1,th:1,s:1", got)
	}
}

func TestBootstrapRejectsUnclusteredEdits(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := db.CreateCollection(t.Context(), editsCollection); err != nil {
		t.Fatalf("create plain message_edits: %v", err)
	}
	if err := Bootstrap(t.Context(), db); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("Bootstrap error = %v, want ErrNotClustered", err)
	}
}

func hasIndex(indexes map[string]bool, pattern string, unique bool) bool {
	u, ok := indexes[pattern]
	return ok && u == unique
}
