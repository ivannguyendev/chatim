package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func reactionReadCases() []interactionCase {
	return []interactionCase{
		{"count returns live emojis of that message only, sorted by count then emoji", reactCount},
		{"between returns reactions and tombstones of a room by time then key", reactBetween},
		{"between orders one instant by _id like the database: shorter keys first", reactBetweenKeyLength},
		{"between pages after a cursor of (updated_at, _id) without repeating or skipping", reactBetweenAfterCursor},
		{"cancelled context writes nothing", reactCancelled},
	}
}

func setAll(t *testing.T, s store.Interactions, rs ...domain.Reaction) {
	t.Helper()
	for _, r := range rs {
		mustSet(t, s, r, true)
	}
}

func reactCount(t *testing.T, s interactionStores) {
	setAll(t, s.interactions,
		reactionOf(roomA, mainThread, 1, "alice", "👍"), reactionOf(roomA, mainThread, 1, "bob", "👍"),
		reactionOf(roomA, mainThread, 1, "carol", "❤️"), reactionOf(roomA, mainThread, 1, "dave", "😂"),
		reactionOf(roomA, mainThread, 1, "erin", "$e"), reactionOf(roomA, mainThread, 1, "frank", "a.b"),
		reactionOf(roomA, mainThread, 2, "alice", "👍"), reactionOf(roomA, sideThread, 1, "bob", "❤️"),
		reactionOf(roomB, mainThread, 1, "carol", "👍"),
	)
	key := msgKey(roomA, mainThread, 1)
	mustRemove(t, s.interactions, key, "dave", baseTime.Add(time.Second), true)
	assertCounts(t, s.interactions, key, []domain.ReactionCount{
		{Emoji: "👍", Count: 2}, {Emoji: "$e", Count: 1}, {Emoji: "a.b", Count: 1}, {Emoji: "❤️", Count: 1},
	})
	assertCounts(t, s.interactions, msgKey(roomA, sideThread, 1), []domain.ReactionCount{{Emoji: "❤️", Count: 1}})
	assertCounts(t, s.interactions, msgKey(roomA, mainThread, 9), nil)
	assertStoredReaction(t, s.interactions, changed(reactionOf(roomA, mainThread, 1, "erin", "$e"), "", 1))
}

func reactBetween(t *testing.T, s interactionStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	mustSet(t, s.interactions, reactAt(roomA, mainThread, 4, "alice", "👍", 0), true)
	atFrom := mustSet(t, s.interactions, reactAt(roomA, mainThread, 5, "alice", "👍", time.Second), true)
	lowKey := mustSet(t, s.interactions, reactAt(roomA, mainThread, 2, "bob", "👍", 2*time.Second), true)
	highBob := mustSet(t, s.interactions, reactAt(roomA, mainThread, 3, "bob", "👍", 2*time.Second), true)
	highCarol := mustSet(t, s.interactions, reactAt(roomA, mainThread, 3, "carol", "❤️", 2*time.Second), true)
	mustSet(t, s.interactions, reactAt(roomA, sideThread, 1, "dave", "😂", 0), true)
	gone := mustRemove(t, s.interactions, msgKey(roomA, sideThread, 1), "dave", to, true)
	mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "erin", "👍", 4*time.Second), true)
	other := mustSet(t, s.interactions, reactAt(roomB, mainThread, 1, "alice", "👍", 2*time.Second), true)
	mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 2, "bob", true, 2*time.Second), true)
	mustAddReply(t, s.interactions, replyAt(roomA, 2, 6, "bob", 2*time.Second), true)
	want := reactionRefs(atFrom, lowKey, highBob, highCarol, gone)
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []store.Interaction
	}{
		{roomA, from, to, store.MaxInteractionScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, reactionRefs(other)},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.interactions.Between(t.Context(), store.InteractionScan{Room: c.room, Kind: keys.ReactionKind, From: c.from, To: c.to, Limit: c.limit})
		if err != nil {
			t.Fatalf("Between(%d, limit %d): %v", c.room, c.limit, err)
		}
		assertInteractions(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func reactBetweenKeyLength(t *testing.T, s interactionStores) {
	at := 2 * time.Second
	carol := mustSet(t, s.interactions, reactAt(roomA, mainThread, 2, "carol", "👍", at), true)
	bob := mustSet(t, s.interactions, reactAt(roomA, mainThread, 3, "bob", "👍", at), true)
	dave := mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "dave", "👍", at), true)
	al := mustSet(t, s.interactions, reactAt(roomA, mainThread, 9, "al", "👍", at), true)
	eve := mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "eve", "👍", at), true)
	got, err := s.interactions.Between(t.Context(), store.InteractionScan{Room: roomA, Kind: keys.ReactionKind, From: baseTime, To: baseTime.Add(time.Hour), Limit: 10})
	if err != nil {
		t.Fatalf("Between: %v", err)
	}
	assertInteractions(t, "Between(one instant)", got, reactionRefs(al, eve, bob, dave, carol))
}

func reactBetweenAfterCursor(t *testing.T, s interactionStores) {
	at := 2 * time.Second
	carol := mustSet(t, s.interactions, reactAt(roomA, mainThread, 2, "carol", "👍", at), true)
	dave := mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "dave", "👍", at), true)
	al := mustSet(t, s.interactions, reactAt(roomA, mainThread, 9, "al", "👍", at), true)
	eve := mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "eve", "👍", at), true)
	later := mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "bo", "👍", at+time.Millisecond), true)
	want := reactionRefs(al, eve, dave, carol, later)
	q := store.InteractionScan{Room: roomA, Kind: keys.ReactionKind, From: baseTime, To: baseTime.Add(time.Hour), Limit: 2}
	var got []store.Interaction
	for range want {
		page, err := s.interactions.Between(t.Context(), q)
		if err != nil {
			t.Fatalf("Between(after %+v): %v", q.After, err)
		}
		got = append(got, page...)
		if len(page) < q.Limit {
			break
		}
		q.After = &page[len(page)-1]
	}
	assertInteractions(t, "Between(paged by cursor)", got, want)
}

func reactCancelled(t *testing.T, s interactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	ctx, key := cancelledContext(t), store.KeyOf(m)
	_, _, err := s.interactions.SetReaction(ctx, reactionOf(roomA, mainThread, 1, "alice", "👍"))
	assertErrorIs(t, "Set", err, context.Canceled)
	_, _, err = s.interactions.RemoveReaction(ctx, key, "alice", baseTime)
	assertErrorIs(t, "Remove", err, context.Canceled)
	_, _, err = s.interactions.GetReaction(ctx, key, "alice")
	assertErrorIs(t, "Get", err, context.Canceled)
	_, err = s.interactions.CountReactions(ctx, key)
	assertErrorIs(t, "CountReactions", err, context.Canceled)
	_, _, err = s.interactions.SetBookmark(ctx, bookmarkAt(roomA, mainThread, 1, "alice", true, 0))
	assertErrorIs(t, "SetBookmark", err, context.Canceled)
	_, err = s.interactions.AddReply(ctx, replyAt(roomA, 1, 2, "alice", 0))
	assertErrorIs(t, "AddReply", err, context.Canceled)
	_, err = s.interactions.RemoveReply(ctx, replyAt(roomA, 1, 2, "alice", 0), baseTime)
	assertErrorIs(t, "RemoveReply", err, context.Canceled)
	_, err = s.interactions.Replies(ctx, key, 0, 10)
	assertErrorIs(t, "Replies", err, context.Canceled)
	_, err = s.interactions.CountLiveReplies(ctx, key)
	assertErrorIs(t, "CountLiveReplies", err, context.Canceled)
	_, err = s.interactions.Bookmarks(ctx, tenant, "alice", store.BookmarkCursor{}, 10)
	assertErrorIs(t, "Bookmarks", err, context.Canceled)
	_, err = s.interactions.Between(ctx, store.InteractionScan{Room: roomA, Kind: keys.ReactionKind, From: baseTime, To: baseTime.Add(time.Hour), Limit: 10})
	assertErrorIs(t, "Between", err, context.Canceled)
	_, err = s.msgs.SetReactions(ctx, key, 0, domain.ReactionSummary{Version: 1})
	assertErrorIs(t, "SetReactions", err, context.Canceled)
	assertNoReaction(t, s.interactions, key, "alice")
	assertStored(t, s.msgs, m)
}
