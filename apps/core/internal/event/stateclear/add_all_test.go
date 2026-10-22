package stateclear_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func targets(seqs ...uint64) []store.StateTarget {
	out := make([]store.StateTarget, len(seqs))
	for i, s := range seqs {
		out[i] = target(s)
	}
	return out
}

func TestAddAllKeepsWhatFitsAndCountsTheRestAsDropped(t *testing.T) {
	rg := newRig(t, smallBuffer)
	rg.AddAll(targets(1, 2, 3, 4, 5))
	rg.expectCounts(t, 0, 3, 0)
	if n := rg.sink.Count(queueFullMsg); n != 1 {
		t.Fatalf("logged a full queue %d times, want 1", n)
	}
	rg.AddAll(nil)
	rg.expectCounts(t, 0, 3, 0)
	if err := rg.Close(t.Context()); err != nil {
		t.Fatalf("Close before Run = %v", err)
	}
	rg.expectCounts(t, 0, 5, 0)
}

func TestAddAllAfterCloseDropsEveryTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.closeAndWait(t)
		rg.AddAll(targets(1, 2, 3))
		rg.expectCounts(t, 0, 3, 0)
	})
}

func TestAddAllTargetsAreClearedInOrderWithSingleAdds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.add(1)
		rg.AddAll(targets(2, 3, 4))
		rg.add(5)
		rg.closeAndWait(t)
		expectSizes(t, rg.st, 5)
		if got := rg.st.seqs(0); !slices.Equal(got, seqRange(1, 5)) {
			t.Fatalf("cleared seqs = %v, want 1..5 in order", got)
		}
		rg.expectCounts(t, 5, 0, 0)
	})
}
