package e2e

import (
	"fmt"
	"slices"
)

type DrillLoss struct {
	Stored    int
	Live      int
	Missing   int
	Covered   int
	Extra     int
	Uncovered []uint64
}

func CheckDrillLoss(stored, live []uint64, gaps []WSEntry) DrillLoss {
	seen := make(map[uint64]bool, len(live))
	for _, s := range live {
		seen[s] = true
	}
	kept := make(map[uint64]bool, len(stored))
	loss := DrillLoss{Stored: len(stored), Live: len(seen)}
	for _, s := range stored {
		kept[s] = true
		if seen[s] {
			continue
		}
		loss.Missing++
		if inGap(s, gaps) {
			loss.Covered++
			continue
		}
		loss.Uncovered = append(loss.Uncovered, s)
	}
	for s := range seen {
		if !kept[s] {
			loss.Extra++
		}
	}
	slices.Sort(loss.Uncovered)
	return loss
}

func (l DrillLoss) Err() error {
	if len(l.Uncovered) == 0 && l.Extra == 0 {
		return nil
	}
	return fmt.Errorf("silent loss: %d stored seqs neither delivered nor reported as a gap (first %v), %d delivered seqs not stored", len(l.Uncovered), l.Uncovered[:min(len(l.Uncovered), 10)], l.Extra)
}

func (l DrillLoss) String() string {
	return fmt.Sprintf("stored %d, live %d, missing %d (%d inside reported gaps, %d silent), %d not stored", l.Stored, l.Live, l.Missing, l.Covered, len(l.Uncovered), l.Extra)
}

func inGap(seq uint64, gaps []WSEntry) bool {
	for _, g := range gaps {
		if g.Frame == FrameGap && seq >= g.Seq && seq <= g.To {
			return true
		}
	}
	return false
}
