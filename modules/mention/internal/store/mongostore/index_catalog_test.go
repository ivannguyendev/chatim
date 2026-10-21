package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

func TestIndexCatalogCoversOwnedCollectionsAndRetiresTheReadIndex(t *testing.T) {
	cat := Indexes()
	if got := cat.Names(); !slices.Equal(got, []string{mentionsCollection, roomsCollection, userGroupsCollection, groupsCollection}) {
		t.Fatalf("collections = %v", got)
	}
	if len(cat.Replacements) != 1 || !cat.Replacements[0].Retired() || cat.Replacements[0].Old != "room_id_1_target_1_thread_root_1_seq_1" {
		t.Fatalf("replacements = %+v, want the read index retired", cat.Replacements)
	}
	if name, err := mongoindex.Name(mongo.IndexModel{Keys: cat.Replacements[0].OldKeys}); err != nil || name != cat.Replacements[0].Old {
		t.Fatalf("old keys name to %q (%v), want %s", name, err, cat.Replacements[0].Old)
	}
}
