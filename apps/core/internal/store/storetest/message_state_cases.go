package storetest

import (
	"cmp"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type StatefulMessages interface {
	store.Messages
	store.MessageCounts
	store.MessageEditor
}

type MessageStateStores struct {
	Messages StatefulMessages
	States   store.EventStates
}

func (s MessageStateStores) states() EventStateStores { return EventStateStores{States: s.States} }

type messageStateCase struct {
	name string
	run  func(t *testing.T, s MessageStateStores)
}

func RunMessageStates(t *testing.T, open func(t *testing.T) MessageStateStores) {
	t.Helper()
	for _, c := range []messageStateCase{
		{"insert flags every new message, system or forwarded, with bit 1 at its send time", insertFlagsMessages},
		{"a duplicate or rejected insert flags nothing", insertDuplicateKeepsFlags},
		{"a count on a flagged message adds its bit and keeps the older flag time", countsKeepOlderFlagTime},
		{"counts flag an unflagged message at the write time", countsFlagAtWriteTime},
		{"a lost CAS, a missing message and an edit flag nothing", countMissesFlagNothing},
		{"clear of a count bit with an older version keeps it", clearCountBitByVersion},
		{"clear of bit 1 over ten messages that skips the fifth leaves the fifth flagged", clearSkipsOneOfTen},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func insertFlagsMessages(t *testing.T, s MessageStateStores) {
	plain, sys, fwd := msg(roomA, mainThread, 1), systemMessage(2), msg(roomA, sideThread, 3)
	fwd.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan", SentAt: baseTime}
	plain.CreatedAt, sys.CreatedAt, fwd.CreatedAt = secs(3), secs(1), secs(2)
	mustInsert(t, s.Messages, []domain.Message{plain, sys, fwd})
	got := allPending(t, s.states(), store.StateMessages)
	if len(got) != 3 {
		t.Fatalf("Pending(messages) = %+v, want 3 docs", got)
	}
	for i, m := range []domain.Message{sys, fwd, plain} {
		want := store.PendingDoc{Collection: store.StateMessages, Room: m.Room, Thread: m.Thread, Seq: m.Seq, State: store.BitMsgCreated, StateAt: m.CreatedAt}
		assertPendingDoc(t, got[i], want)
	}
}

func insertDuplicateKeepsFlags(t *testing.T, s MessageStateStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.Messages, []domain.Message{m})
	mustClearStates(t, s.states(), msgState(1, store.BitMsgCreated, 0))
	again := m
	again.CreatedAt, again.CID = secs(9), "cid-again"
	res := s.Messages.Insert(t.Context(), []domain.Message{again, msg(roomA, mainThread, 0)})
	if res[0].Outcome != store.Duplicate || res[1].Outcome != store.Rejected {
		t.Fatalf("Insert(duplicate, zero seq) = %+v, want Duplicate and Rejected", res)
	}
	assertNoPending(t, s.states(), store.StateMessages)
}

func countsKeepOlderFlagTime(t *testing.T, s MessageStateStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.Messages, []domain.Message{m})
	key := store.KeyOf(m)
	if _, err := s.Messages.AddReactionCounts(t.Context(), key, []store.EmojiDelta{delta("👍", 1)}); err != nil {
		t.Fatalf("AddReactionCounts: %v", err)
	}
	want := store.PendingDoc{Collection: store.StateMessages, Room: roomA, Seq: 1, State: store.BitMsgCreated | store.BitReactionCounts, StateAt: baseTime}
	assertPendingDoc(t, onlyPending(t, s), want)
	if _, err := s.Messages.AddReplyCount(t.Context(), key, 1); err != nil {
		t.Fatalf("AddReplyCount: %v", err)
	}
	want.State |= store.BitReplyCounts
	assertPendingDoc(t, onlyPending(t, s), want)
}

func countsFlagAtWriteTime(t *testing.T, s MessageStateStores) {
	mustInsert(t, s.Messages, span(roomA, mainThread, 1, 4))
	mustClearStates(t, s.states(), msgStates(store.BitMsgCreated, 1, 2, 3, 4)...)
	before := time.Now()
	if _, err := s.Messages.AddReactionCounts(t.Context(), msgKey(roomA, mainThread, 1), []store.EmojiDelta{delta("👍", 1)}); err != nil {
		t.Fatalf("AddReactionCounts: %v", err)
	}
	mustSetReactions(t, s.Messages, msgKey(roomA, mainThread, 2), 0, summary(1, emojiCount("$e", 1)), true)
	if _, err := s.Messages.AddReplyCount(t.Context(), msgKey(roomA, mainThread, 3), 1); err != nil {
		t.Fatalf("AddReplyCount: %v", err)
	}
	if ok, err := s.Messages.SetReplyCount(t.Context(), msgKey(roomA, mainThread, 4), 0, 2); err != nil || !ok {
		t.Fatalf("SetReplyCount = %v, %v; want true", ok, err)
	}
	after := time.Now()
	got := allPending(t, s.states(), store.StateMessages)
	slices.SortFunc(got, func(a, b store.PendingDoc) int { return cmp.Compare(a.Seq, b.Seq) })
	wantBits := []store.StateBit{store.BitReactionCounts, store.BitReactionCounts, store.BitReplyCounts, store.BitReplyCounts}
	if len(got) != len(wantBits) {
		t.Fatalf("Pending(messages) = %+v, want seqs 1..4", got)
	}
	for i, d := range got {
		if d.Seq != uint64(i+1) || d.State != wantBits[i] || !aroundWrite(d.StateAt, before, after) {
			t.Fatalf("pending[%d] = %+v, want seq %d bit %d flagged between %v and %v", i, d, i+1, wantBits[i], before, after)
		}
	}
}

func aroundWrite(at, before, after time.Time) bool {
	const skew = 2 * time.Second
	return !at.Before(before.Add(-skew)) && !at.After(after.Add(skew))
}

func onlyPending(t *testing.T, s MessageStateStores) store.PendingDoc {
	t.Helper()
	got := allPending(t, s.states(), store.StateMessages)
	if len(got) != 1 {
		t.Fatalf("Pending(messages) = %+v, want one doc", got)
	}
	return got[0]
}

func msgStates(bit store.StateBit, seqs ...uint64) []store.StateTarget {
	out := make([]store.StateTarget, len(seqs))
	for i, seq := range seqs {
		out[i] = msgState(seq, bit, 0)
	}
	return out
}
