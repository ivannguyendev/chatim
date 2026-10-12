package effects_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestARepairCleansStoredReactionEntriesAtOrBelowZero(t *testing.T) {
	rg := newCountRig(t)
	rg.react(t, 1, "bob", "👍")
	rg.addReactions(t, 1, store.EmojiDelta{Emoji: "👍", Delta: 1}, store.EmojiDelta{Emoji: "😮", Delta: -1})
	if before := rg.stored(t, 1).Reactions; !before.Unsettled || !slices.Equal(before.Counts, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("before = %+v, want 👍 once with a leftover entry below zero", before)
	}
	if errs := rg.run(t, countRec(room, 1, pbconv.ReactionsCounter, 7)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	got := rg.stored(t, 1).Reactions
	if got.Unsettled || got.Version != 2 || !slices.Equal(got.Counts, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) || rg.repair.Repaired(pbconv.ReactionsCounter) != 1 {
		t.Fatalf("after = %+v, repaired %d; want the recount written at version 2", got, rg.repair.Repaired(pbconv.ReactionsCounter))
	}
}

func TestARepairCleansAStoredReplyCountBelowZero(t *testing.T) {
	rg := newCountRig(t)
	if _, err := rg.msgs.AddReplyCount(t.Context(), store.MsgKey{Room: room, Seq: 1}, -1); err != nil {
		t.Fatalf("AddReplyCount: %v", err)
	}
	if errs := rg.run(t, countRec(room, 1, pbconv.RepliesCounter, 7)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.stored(t, 1).Replies; got != (domain.ReplyCount{N: 0, Version: 2}) || rg.repair.Repaired(pbconv.RepliesCounter) != 1 {
		t.Fatalf("after = %+v, repaired %d; want 0 rewritten at version 2", got, rg.repair.Repaired(pbconv.RepliesCounter))
	}
}
