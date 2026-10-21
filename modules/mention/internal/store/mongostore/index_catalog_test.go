package mongostore

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

func TestIndexCatalogCoversOwnedCollectionsAndRetiresTheReadIndex(t *testing.T) {
	cat := Indexes()
	if got := cat.Names(); !slices.Equal(got, []string{mentionsCollection, roomsCollection, userGroupsCollection, groupsCollection}) {
		t.Fatalf("collections = %v", got)
	}
	want := []mongoindex.Replacement{{Collection: mentionsCollection, Old: "room_id_1_target_1_thread_root_1_seq_1"}}
	if !slices.Equal(cat.Replacements, want) || !cat.Replacements[0].Retired() {
		t.Fatalf("replacements = %+v, want %+v retired", cat.Replacements, want)
	}
}
