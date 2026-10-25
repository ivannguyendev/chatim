package corecall

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

func TestHistogramIsCumulativeWithAnOverflowCount(t *testing.T) {
	s := newStats([]string{"A", "B"})
	s.observe(1, 30*time.Millisecond)
	s.observe(1, 5*time.Second)
	s.observe(1, 20*time.Second)
	s.observe(1, -time.Second)
	h := s.histogram(1)
	want := []uint64{1, 1, 1, 2, 2, 2, 2, 2, 2, 3, 3}
	if !slices.Equal(h.Cumulative, want) || h.Count != 4 || h.Sum != 25.03 {
		t.Fatalf("histogram = %+v", h)
	}
	if other := s.histogram(0); other.Count != 0 || other.Cumulative[len(other.Cumulative)-1] != 0 {
		t.Fatalf("other command = %+v", other)
	}
}

func TestCountersAreKeptPerCommandAndCode(t *testing.T) {
	s := newStats([]string{"A", "B"})
	s.count(0, codes.Unauthenticated)
	s.count(1, codes.OK)
	s.count(1, codes.OK)
	reads := map[string]float64{}
	for _, src := range s.commandSources() {
		reads[src.Labels["command"]+"/"+src.Labels["code"]] = src.Read()
	}
	if len(reads) != 2*codeCount || reads["A/Unauthenticated"] != 1 || reads["B/OK"] != 2 || reads["A/OK"] != 0 {
		t.Fatalf("reads = %v", reads)
	}
}
