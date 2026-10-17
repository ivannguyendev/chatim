package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func clearKeepsOtherBits(t *testing.T, s EventStateStores) {
	mustSeedState(t, s, secs(1), msgState(1, store.BitMsgCreated, 0), msgState(1, store.BitReactionCounts, 3))
	mustSeedState(t, s, secs(2), memberState("bob", store.BitMember, 4, time.Time{}), memberState("bob", store.BitReadPosition, 7, time.Time{}))
	mustClearStates(t, s, msgState(1, store.BitMsgCreated, 0), memberState("bob", store.BitReadPosition, 7, time.Time{}))
	msgs := allPending(t, s, store.StateMessages)
	if len(msgs) != 1 {
		t.Fatalf("Pending(messages) = %+v, want one doc", msgs)
	}
	assertPendingDoc(t, msgs[0], store.PendingDoc{Collection: store.StateMessages, Room: roomA, Seq: 1, State: store.BitReactionCounts, StateAt: secs(1)})
	members := allPending(t, s, store.StateMembers)
	if len(members) != 1 {
		t.Fatalf("Pending(members) = %+v, want one doc", members)
	}
	assertPendingDoc(t, members[0], store.PendingDoc{Collection: store.StateMembers, Room: roomA, User: "bob", State: store.BitMember, StateAt: secs(2)})
}

func versionedStates() []store.StateTarget {
	return []store.StateTarget{
		msgState(1, store.BitReactionCounts, 3),
		msgState(2, store.BitReplyCounts, 5),
		interactionState(keys.ReactionKind, "bob", 2),
		interactionState(keys.BookmarkKind, "bob", 4),
		memberState("bob", store.BitMember, 6, time.Time{}),
		memberState("bob", store.BitReadPosition, 7, time.Time{}),
		memberState("bob", store.BitHistoryCleared, 0, secs(40).Add(250*time.Millisecond)),
		memberState("bob", store.BitPreferences, 2, time.Time{}),
		roomState(roomA, store.BitMemberCount, 3),
		roomState(roomA, store.BitRoomInfo, 2),
	}
}

func clearStaleVersion(t *testing.T, s EventStateStores) {
	targets := versionedStates()
	mustSeedState(t, s, secs(1), targets...)
	stale := make([]store.StateTarget, len(targets))
	for i, target := range targets {
		stale[i] = target
		if target.Bit == store.BitHistoryCleared && target.Collection == store.StateMembers {
			stale[i].At = target.At.Add(-time.Millisecond)
		} else {
			stale[i].Ver = target.Ver - 1
		}
	}
	mustClearStates(t, s, stale...)
	for _, c := range []store.StateCollection{store.StateMessages, store.StateInteractions, store.StateMembers, store.StateRooms} {
		if len(allPending(t, s, c)) == 0 {
			t.Fatalf("Pending(%s) is empty after clears with other versions", c)
		}
	}
	mustClearStates(t, s, targets...)
	for _, c := range []store.StateCollection{store.StateMessages, store.StateInteractions, store.StateMembers, store.StateRooms} {
		assertNoPending(t, s, c)
	}
}

func clearLastBit(t *testing.T, s EventStateStores) {
	mustSeedState(t, s, secs(1), msgState(1, store.BitMsgCreated, 0), msgState(2, store.BitMsgCreated, 0), roomState(roomA, store.BitRoomCreated, 0))
	mustClearStates(t, s,
		msgState(1, store.BitMsgCreated, 0),
		msgState(2, store.BitReactionCounts, 0),
		msgState(99, store.BitMsgCreated, 0),
		roomState(roomB, store.BitRoomCreated, 0),
		memberState("ghost", store.BitMember, 1, time.Time{}),
		pinState(7),
	)
	got := allPending(t, s, store.StateMessages)
	if len(got) != 1 {
		t.Fatalf("Pending(messages) = %+v, want only seq 2", got)
	}
	assertPendingDoc(t, got[0], store.PendingDoc{Collection: store.StateMessages, Room: roomA, Seq: 2, State: store.BitMsgCreated, StateAt: secs(1)})
	mustClearStates(t, s, msgState(2, store.BitMsgCreated, 0), msgState(2, store.BitMsgCreated, 0))
	assertNoPending(t, s, store.StateMessages)
	mustSeedState(t, s, secs(9), msgState(2, store.BitReplyCounts, 1))
	got = allPending(t, s, store.StateMessages)
	if len(got) != 1 || !got[0].StateAt.Equal(secs(9)) || got[0].State != store.BitReplyCounts {
		t.Fatalf("Pending after a new bit on a cleared doc = %+v, want seq 2 at %v", got, secs(9))
	}
}

func clearManyCollections(t *testing.T, s EventStateStores) {
	targets := []store.StateTarget{
		msgState(1, store.BitMsgCreated, 0), editState(1, 1), interactionState(keys.ReactionKind, "bob", 1),
		pinState(1), memberState("bob", store.BitMember, 1, time.Time{}), hiddenState("bob", 1), roomState(roomA, store.BitRoomCreated, 0),
	}
	mustSeedState(t, s, secs(1), targets...)
	mustClearStates(t, s, targets...)
	for _, c := range store.AllStateCollections() {
		assertNoPending(t, s, c)
	}
}

func markExistingDocs(t *testing.T, s EventStateStores) {
	member := memberState("bob", store.BitMember, 4, time.Time{})
	edit := editState(3, 2)
	mustSeedState(t, s, secs(1), member, edit)
	mustSeedState(t, s, secs(2), roomState(roomA, store.BitMemberCount, 3))
	mustClearStates(t, s, member, edit)
	start := time.Now().Add(-time.Minute)
	marks := map[store.StateCollection][]store.StateTarget{
		store.StateMembers: {member, memberState("bob", store.BitPreferences, 9, time.Time{}), memberState("ghost", store.BitMember, 1, time.Time{})},
		store.StateEdits:   {edit, editState(3, 5)},
		store.StateRooms:   {roomState(roomA, store.BitRoomCreated, 0), roomState(roomB, store.BitRoomCreated, 0)},
	}
	for c, targets := range marks {
		if err := s.States.Mark(t.Context(), c, targets); err != nil {
			t.Fatalf("Mark(%s, %+v): %v", c, targets, err)
		}
	}
	end := time.Now().Add(time.Minute)
	assertMarked(t, s, store.StateMembers, store.BitMember, start, end)
	assertMarked(t, s, store.StateEdits, store.BitEditFact, start, end)
	rooms := allPending(t, s, store.StateRooms)
	if len(rooms) != 1 {
		t.Fatalf("Pending(rooms) = %+v, want one doc", rooms)
	}
	assertPendingDoc(t, rooms[0], store.PendingDoc{Collection: store.StateRooms, Room: roomA, State: store.BitRoomCreated | store.BitMemberCount, StateAt: secs(2)})
}

func assertMarked(t *testing.T, s EventStateStores, c store.StateCollection, bit store.StateBit, start, end time.Time) {
	t.Helper()
	got := allPending(t, s, c)
	if len(got) != 1 || got[0].State != bit || got[0].StateAt.Before(start) || !got[0].StateAt.Before(end) {
		t.Fatalf("Pending(%s) after Mark = %+v, want one doc with bit %b flagged between %v and %v", c, got, bit, start, end)
	}
}
