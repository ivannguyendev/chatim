package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func countMissesFlagNothing(t *testing.T, s MessageStateStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.Messages, []domain.Message{m})
	mustClearStates(t, s.states(), msgState(1, store.BitMsgCreated, 0))
	key := store.KeyOf(m)
	mustSetReactions(t, s.Messages, key, 3, summary(4, emojiCount("👍", 1)), false)
	if ok, err := s.Messages.SetReplyCount(t.Context(), key, 5, 2); err != nil || ok {
		t.Fatalf("SetReplyCount(stale base) = %v, %v; want false", ok, err)
	}
	missing := msgKey(roomA, mainThread, 2)
	_, err := s.Messages.AddReactionCounts(t.Context(), missing, []store.EmojiDelta{delta("👍", 1)})
	assertErrorIs(t, "AddReactionCounts(missing)", err, domain.ErrMessageNotFound)
	_, err = s.Messages.AddReplyCount(t.Context(), missing, 1)
	assertErrorIs(t, "AddReplyCount(missing)", err, domain.ErrMessageNotFound)
	mustApply(t, s.Messages, fact(roomA, mainThread, 1, 1))
	assertNoPending(t, s.states(), store.StateMessages)
}

func clearCountBitByVersion(t *testing.T, s MessageStateStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.Messages, []domain.Message{m})
	key := store.KeyOf(m)
	mustAddReactions(t, s.Messages, key, summary(1, emojiCount("👍", 1)), delta("👍", 1))
	mustAddReactions(t, s.Messages, key, summary(2, emojiCount("👍", 2)), delta("👍", 1))
	if _, err := s.Messages.AddReplyCount(t.Context(), key, 1); err != nil {
		t.Fatalf("AddReplyCount: %v", err)
	}
	mustClearStates(t, s.states(), msgState(1, store.BitMsgCreated, 0), msgState(1, store.BitReactionCounts, 1), msgState(1, store.BitReplyCounts, 0))
	if got := onlyPending(t, s).State; got != store.BitReactionCounts|store.BitReplyCounts {
		t.Fatalf("state after stale clears = %d, want bits 2 and 4", got)
	}
	mustClearStates(t, s.states(), msgState(1, store.BitReactionCounts, 2))
	if got := onlyPending(t, s).State; got != store.BitReplyCounts {
		t.Fatalf("state after the current reactions clear = %d, want bit 4", got)
	}
	mustClearStates(t, s.states(), msgState(1, store.BitReplyCounts, 1))
	assertNoPending(t, s.states(), store.StateMessages)
}

func clearSkipsOneOfTen(t *testing.T, s MessageStateStores) {
	mustInsert(t, s.Messages, span(roomA, mainThread, 1, 10))
	mustClearStates(t, s.states(), msgStates(store.BitMsgCreated, 1, 2, 3, 4, 6, 7, 8, 9, 10)...)
	got := allPending(t, s.states(), store.StateMessages)
	if !slices.Equal(pendingSeqs(got), []uint64{5}) || got[0].State != store.BitMsgCreated {
		t.Fatalf("Pending(messages) = %+v, want only seq 5 with bit 1", got)
	}
}
