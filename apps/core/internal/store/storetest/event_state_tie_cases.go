package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func pendingMembersTieByKeyLength(t *testing.T, s EventStateStores) {
	other := memberState("b", store.BitMember, 1, secs(0))
	other.Room = roomB
	mustSeedState(t, s, secs(2),
		memberState("carol", store.BitMember, 1, secs(0)), memberState("al", store.BitReadPosition, 2, secs(0)),
		memberState("zed", store.BitPreferences, 1, secs(0)), other, memberState("a", store.BitMember, 1, secs(0)),
		memberState("bob", store.BitMember, 1, secs(0)))
	want := []string{"a", "b", "al", "bob", "zed", "carol"}
	for _, limit := range []int{1, 2, 4} {
		var got []string
		for after := (store.PendingCursor{}); ; {
			docs, next := pendingPage(t, s, store.StateMembers, secs(2), secs(3), after, limit)
			for _, d := range docs {
				got = append(got, d.User)
			}
			if next.IsZero() {
				break
			}
			after = next
		}
		if !slices.Equal(got, want) {
			t.Fatalf("members paged by %d = %v, want %v", limit, got, want)
		}
	}
}
