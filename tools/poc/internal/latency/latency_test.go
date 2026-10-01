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
	want := Summary{Count: 100, P50: 50 * time.Millisecond, P95: 95 * time.Millisecond, P99: 99 * time.Millisecond, Max: 100 * time.Millisecond}
	if s != want {
		t.Fatalf("Summary() = %+v, want %+v", s, want)
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
