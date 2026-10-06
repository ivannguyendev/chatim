package view_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
)

var thumbs = []domain.ReactionCount{{Emoji: "👍", Count: 2}}

func reacted(seq uint64, deleted bool) domain.Message {
	return domain.Message{
		Room: 7, Seq: seq, From: "alice", Text: "t", Deleted: deleted,
		Reactions: domain.ReactionSummary{Counts: slices.Clone(thumbs), Version: 3},
	}
}

func counted(m domain.Message) bool {
	return m.Reactions.Version == 3 && slices.Equal(m.Reactions.Counts, thumbs)
}

func uncounted(m domain.Message) bool {
	return m.Reactions.Version == 0 && m.Reactions.Counts == nil
}

func TestPlaceholdersCarryNoReactionCounts(t *testing.T) {
	page := []domain.Message{reacted(1, false), reacted(2, true), reacted(3, false)}
	masked := view.MaskDeleted(view.Viewer{}, page)
	if !counted(masked[0]) || !uncounted(masked[1]) || !counted(masked[2]) {
		t.Fatalf("MaskDeleted reactions = %+v, want only the deleted seq 2 without counts", masked)
	}
	hidden := view.HideForViewer(view.Viewer{ClearedBeforeSeq: 1, HiddenSeqs: map[uint64]bool{3: true}}, page)
	if !uncounted(hidden[0]) || !counted(hidden[1]) || !uncounted(hidden[2]) {
		t.Fatalf("HideForViewer reactions = %+v, want seq 1 and 3 without counts", hidden)
	}
	for _, m := range page {
		if !counted(m) {
			t.Fatalf("input page was modified: %+v", m)
		}
	}
}
