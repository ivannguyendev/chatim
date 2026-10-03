package latency

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

func TestSummaryNearestRank(t *testing.T) {
	var r Recorder
	for _, i := range rand.Perm(100) {
		r.Add(time.Duration(i+1) * time.Millisecond)
	}
	s := r.Summary()
	want := Summary{Count: 100, P50: 50 * time.Millisecond, P95: 95 * time.Millisecond, P99: 99 * time.Millisecond, P999: 100 * time.Millisecond, Max: 100 * time.Millisecond}
	if s != want {
		t.Fatalf("Summary() = %+v, want %+v", s, want)
	}
}

func TestSummaryP999NeedsAThousandSamples(t *testing.T) {
	var r Recorder
	for _, i := range rand.Perm(2000) {
		r.Add(time.Duration(i+1) * time.Microsecond)
	}
	s := r.Summary()
	if s.P99 != 1980*time.Microsecond || s.P999 != 1998*time.Microsecond || s.Max != 2000*time.Microsecond {
		t.Fatalf("Summary() = %+v, want p99=1.98ms p99.9=1.998ms max=2ms", s)
	}
	if got := s.String(); got != "n=2000 p50=1ms p95=1.9ms p99=1.98ms p99.9=1.998ms max=2ms" {
		t.Fatalf("String() = %q", got)
	}
}

func TestSummaryEdgeCases(t *testing.T) {
	var r Recorder
	if s := r.Summary(); s != (Summary{}) {
		t.Fatalf("empty Summary() = %+v", s)
	}
	r.Add(7 * time.Millisecond)
	if s := r.Summary(); s.P50 != 7*time.Millisecond || s.P99 != 7*time.Millisecond || s.Count != 1 {
		t.Fatalf("single-sample Summary() = %+v", s)
	}
	r.Add(3 * time.Millisecond)
	r.Add(9 * time.Millisecond)
	if s := r.SummaryAndReset(); s.Count != 3 {
		t.Fatalf("SummaryAndReset() = %+v, want Count 3", s)
	}
	if r.Summary().Count != 0 {
		t.Fatal("SummaryAndReset must drop samples")
	}
}

func TestRecorderIsSafeForConcurrentUse(t *testing.T) {
	var r Recorder
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 100 {
				r.Add(time.Millisecond)
			}
		})
	}
	wg.Wait()
	if n := r.Summary().Count; n != 1000 {
		t.Fatalf("Count = %d, want 1000", n)
	}
}
