package mongostore

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

func declaredNames(t *testing.T, cat mongoindex.Catalog, coll string) []string {
	t.Helper()
	for _, c := range cat.Collections {
		if c.Name != coll {
			continue
		}
		names := make([]string, len(c.Indexes))
		for i, m := range c.Indexes {
			name, err := mongoindex.Name(m)
			if err != nil {
				t.Fatalf("Name: %v", err)
			}
			names[i] = name
		}
		return names
	}
	t.Fatalf("collection %s not declared", coll)
	return nil
}

func TestIndexCatalogDeclaresTheMembersUserIndexWithVer(t *testing.T) {
	want := "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1_ver_1"
	if membersUserIndex != want {
		t.Fatalf("membersUserIndex = %s, want %s", membersUserIndex, want)
	}
	if got := declaredNames(t, Indexes(), membersCollection); !slices.Contains(got, want) {
		t.Fatalf("members indexes = %v, want %s", got, want)
	}
}

func TestIndexCatalogReplacementsPointAtDeclaredIndexes(t *testing.T) {
	cat := Indexes()
	want := []mongoindex.Replacement{
		{Collection: membersCollection, Old: "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1", New: membersUserIndex},
		{Collection: membersCollection, Old: "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1", New: membersUserIndex},
		{Collection: interactionsCollection, Old: "message_key_1_kind_1_state_1_value_1", New: "message_key_1_kind_1_state_1_value_1_updated_at_-1_actor_id_1"},
	}
	if !slices.Equal(cat.Replacements, want) {
		t.Fatalf("replacements = %+v, want %+v", cat.Replacements, want)
	}
	for _, r := range cat.Replacements {
		names := declaredNames(t, cat, r.Collection)
		if !slices.Contains(names, r.New) || slices.Contains(names, r.Old) {
			t.Errorf("replacement %+v: declared %v, want the new one declared and the old one not", r, names)
		}
	}
}

func TestIndexCatalogCoversEveryBootstrappedCollection(t *testing.T) {
	cat := Indexes()
	want := []string{messagesCollection, editsCollection, interactionsCollection, pinActionsCollection, membersCollection, roomDMsCollection, replyThreadsCollection, roomsCollection, hiddenCollection, sweeperStateCollection}
	if got := cat.Names(); !slices.Equal(got, want) {
		t.Fatalf("collections = %v, want %v", got, want)
	}
	for _, c := range store.AllStateCollections() {
		if got := declaredNames(t, cat, c.String()); !slices.Contains(got, statePendingIndex) {
			t.Errorf("%s indexes = %v, want %s", c, got, statePendingIndex)
		}
	}
}
