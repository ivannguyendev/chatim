package storetest

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func replyCases() []interactionCase {
	return []interactionCase{
		{"add reply inserts once and lists the replies of that message by seq", replyAddAndList},
		{"remove reply marks it removed once and drops it from lists and counts", replyRemove},
		{"remove before add leaves a removed reply that add never revives", replyRemoveBeforeAdd},
		{"invalid replies, targets and limits are rejected", replyInvalid},
	}
}

func replyAddAndList(t *testing.T, s interactionStores) {
	parent, other := msgKey(roomA, mainThread, 1), msgKey(roomA, mainThread, 2)
	sixteen := strings.Repeat("u", 16)
	mustSet(t, s.interactions, reactionOf(roomA, mainThread, 1, sixteen, "👍"), true)
	mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 1, sixteen, true, 0), true)
	r5, r3, r9 := replyAt(roomA, 1, 5, "bob", time.Second), replyAt(roomA, 1, 3, "alice", 2*time.Second), replyAt(roomA, 1, 9, "carol", 0)
	for _, r := range []domain.Reply{r5, r3, r9} {
		mustAddReply(t, s.interactions, r, true)
	}
	again := r3
	again.From, again.At = "dave", baseTime.Add(time.Hour)
	mustAddReply(t, s.interactions, again, false)
	toOther := replyAt(roomA, 2, 4, "bob", 0)
	mustAddReply(t, s.interactions, toOther, true)
	mustAddReply(t, s.interactions, replyAt(roomB, 1, 3, "bob", 0), true)
	assertReplies(t, s.interactions, parent, 0, 10, live(r3), live(r5), live(r9))
	assertReplies(t, s.interactions, parent, 3, 10, live(r5), live(r9))
	assertReplies(t, s.interactions, parent, 0, 2, live(r3), live(r5))
	assertReplies(t, s.interactions, parent, 9, 10)
	assertReplies(t, s.interactions, other, 0, 10, live(toOther))
	assertLiveReplies(t, s.interactions, parent, 3)
	assertLiveReplies(t, s.interactions, msgKey(roomA, mainThread, 3), 0)
	assertCounts(t, s.interactions, parent, []domain.ReactionCount{{Emoji: "👍", Count: 1}})
}

func replyRemove(t *testing.T, s interactionStores) {
	parent := msgKey(roomA, mainThread, 1)
	r3, r5 := replyAt(roomA, 1, 3, "alice", 0), replyAt(roomA, 1, 5, "bob", 0)
	mustAddReply(t, s.interactions, r3, true)
	mustAddReply(t, s.interactions, r5, true)
	mustRemoveReply(t, s.interactions, r3, true)
	mustRemoveReply(t, s.interactions, r3, false)
	mustRemoveReply(t, s.interactions, replyAt(roomA, 2, 5, "bob", 0), false)
	assertReplies(t, s.interactions, parent, 0, 10, live(r5))
	assertLiveReplies(t, s.interactions, parent, 1)
	mustAddReply(t, s.interactions, r3, false)
	assertReplies(t, s.interactions, parent, 0, 10, live(r5))
	assertLiveReplies(t, s.interactions, parent, 1)
}

func replyRemoveBeforeAdd(t *testing.T, s interactionStores) {
	parent, r3 := msgKey(roomA, mainThread, 1), replyAt(roomA, 1, 3, "alice", 0)
	mustRemoveReply(t, s.interactions, r3, false)
	mustAddReply(t, s.interactions, r3, false)
	mustRemoveReply(t, s.interactions, r3, false)
	assertReplies(t, s.interactions, parent, 0, 10)
	assertLiveReplies(t, s.interactions, parent, 0)
	from, to := baseTime, baseTime.Add(2*time.Hour)
	got, err := s.interactions.Between(t.Context(), store.InteractionScan{Room: roomA, Kind: keys.ReplyKind, From: from, To: to, Limit: 10})
	want := []store.Interaction{{Kind: keys.ReplyKind, Key: parent, User: "alice", Ver: 1, At: baseTime.Add(time.Hour), Reply: store.ReplyKeyOf(r3)}}
	if err != nil {
		t.Fatalf("Between(replies): %v", err)
	}
	assertInteractions(t, "Between(replies)", got, want)
}

func replyInvalid(t *testing.T, s interactionStores) {
	for name, mutate := range map[string]func(*domain.Reply){
		"zero parent seq":   func(r *domain.Reply) { r.Parent.Seq = 0 },
		"zero reply seq":    func(r *domain.Reply) { r.Seq = 0 },
		"reply in a thread": func(r *domain.Reply) { r.Thread = sideThread },
		"parent in thread":  func(r *domain.Reply) { r.Parent.Thread = sideThread },
		"another room":      func(r *domain.Reply) { r.Room = roomB },
		"reply to itself":   func(r *domain.Reply) { r.Seq = r.Parent.Seq },
		"empty tenant":      func(r *domain.Reply) { r.Tenant = "" },
		"author with a dot": func(r *domain.Reply) { r.From = "a.b" },
		"zero time":         func(r *domain.Reply) { r.At = time.Time{} },
	} {
		r := replyAt(roomA, 1, 2, "alice", 0)
		mutate(&r)
		_, err := s.interactions.AddReply(t.Context(), r)
		assertErrorIs(t, "AddReply("+name+")", err, apperr.ErrInvalidArgument)
		_, err = s.interactions.RemoveReply(t.Context(), r, baseTime)
		assertErrorIs(t, "RemoveReply("+name+")", err, apperr.ErrInvalidArgument)
	}
	parent := msgKey(roomA, mainThread, 1)
	_, err := s.interactions.RemoveReply(t.Context(), replyAt(roomA, 1, 2, "alice", 0), time.Time{})
	assertErrorIs(t, "RemoveReply(zero time)", err, apperr.ErrInvalidArgument)
	assertLiveReplies(t, s.interactions, parent, 0)
	for _, limit := range []int{0, store.MaxPageLimit + 1} {
		_, err := s.interactions.Replies(t.Context(), parent, 0, limit)
		assertErrorIs(t, "Replies(bad limit)", err, apperr.ErrInvalidArgument)
	}
	_, err = s.interactions.CountLiveReplies(t.Context(), msgKey(roomA, mainThread, 0))
	assertErrorIs(t, "CountLiveReplies(zero seq)", err, apperr.ErrInvalidArgument)
	assertReplies(t, s.interactions, parent, 0, 10)
}
