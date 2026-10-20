package roomgroups

import (
	"testing"
	"testing/synctest"
)

func expectStats(t *testing.T, s *Sets, hits, misses uint64) {
	t.Helper()
	if s.Hits() != hits || s.Misses() != misses {
		t.Fatalf("hits %d misses %d, want %d and %d", s.Hits(), s.Misses(), hits, misses)
	}
}

func TestSetsCountHitsAndMisses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := newSets(t)
		of(t, s, smallRoom, 1)
		expectStats(t, s, 0, 1)
		of(t, s, smallRoom, 1)
		expectStats(t, s, 1, 1)
		of(t, s, bigRoom, syncMax+1)
		expectStats(t, s, 1, 2)
		synctest.Wait()
		of(t, s, bigRoom, syncMax+1)
		expectStats(t, s, 2, 2)
	})
}
