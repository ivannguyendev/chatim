package e2e_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func seqs(from, to uint64) []uint64 {
	var out []uint64
	for s := from; s <= to; s++ {
		out = append(out, s)
	}
	return out
}

func TestCheckDrillLossAcceptsEverySeqDelivered(t *testing.T) {
	loss := e2e.CheckDrillLoss(seqs(1, 50), append(seqs(1, 50), 7), nil)
	if loss.Err() != nil || loss.Missing != 0 || loss.Live != 50 || loss.Stored != 50 {
		t.Fatalf("loss = %v, err %v", loss, loss.Err())
	}
}

func TestCheckDrillLossCountsMissingSeqsInsideReportedGaps(t *testing.T) {
	live := append(seqs(1, 10), seqs(31, 40)...)
	gaps := []e2e.WSEntry{{Frame: e2e.FrameGap, Room: "7", Seq: 11, To: 30}}
	loss := e2e.CheckDrillLoss(seqs(1, 40), live, gaps)
	if loss.Err() != nil || loss.Missing != 20 || loss.Covered != 20 {
		t.Fatalf("loss = %v, err %v", loss, loss.Err())
	}
}

func TestCheckDrillLossRefusesSilentLoss(t *testing.T) {
	live := append(seqs(1, 10), seqs(13, 20)...)
	gaps := []e2e.WSEntry{{Frame: e2e.FrameResync, Seq: 1, To: 20}, {Frame: e2e.FrameGap, Seq: 11, To: 11}}
	loss := e2e.CheckDrillLoss(seqs(1, 20), live, gaps)
	if loss.Err() == nil || !slices.Equal(loss.Uncovered, []uint64{12}) || loss.Covered != 1 {
		t.Fatalf("loss = %v, uncovered %v", loss, loss.Uncovered)
	}
}

func TestCheckDrillLossRefusesSeqsNeverStored(t *testing.T) {
	loss := e2e.CheckDrillLoss(seqs(1, 3), seqs(1, 4), nil)
	if loss.Err() == nil || loss.Extra != 1 {
		t.Fatalf("loss = %v, err %v", loss, loss.Err())
	}
}
