package mentionapp_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
)

func TestIndexesExportsTheMentionCollections(t *testing.T) {
	cat := mentionapp.Indexes()
	if got := cat.Names(); !slices.Equal(got, []string{"mentions", "mention_rooms", "mention_user_groups", "mention_groups"}) {
		t.Fatalf("Indexes().Names() = %v", got)
	}
	if len(cat.Replacements) != 1 || cat.Replacements[0].Collection != "mentions" {
		t.Fatalf("Indexes().Replacements = %+v, want the retired mentions read index", cat.Replacements)
	}
}
