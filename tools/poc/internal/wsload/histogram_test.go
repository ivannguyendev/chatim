package wsload

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

func TestHistogramIsExactBelowTheLinearLimit(t *testing.T) {
	var h Histogram
	for _, i := range rand.Perm(100) {
		h.Add(time.Duration(i+1) * time.Microsecond)
	}
	want := Summary{Count: 100, P50: 50 * time.Microsecond, P90: 90 * time.Microsecond, P99: 99 * time.Microsecond, Max: 100 * time.Microsecond}
	if got := h.Summary(); got != want {
		t.Fatalf("Summary() = %+v, want %+v", got, want)
	}
}

func TestHistogramPercentilesStayWithinTwoPercent(t *testing.T) {
	var h Histogram
	for _, i := range rand.Perm(10_000) {
		h.Add(time.Duration(i+1) * 100 * time.Microsecond)
	}
	s := h.Summary()
	checks := []struct {
		name      string
		got, want time.Duration
	}{
		{"p50", s.P50, 500 * time.Millisecond},
		{"p90", s.P90, 900 * time.Millisecond},
		{"p99", s.P99, 990 * time.Millisecond},
	}
	for _, c := range checks {
		if c.got < c.want || float64(c.got-c.want) > 0.02*float64(c.want) {
			t.Errorf("%s = %v, want %v within +2%%", c.name, c.got, c.want)
		}
	}
	if s.Max != time.Second || s.Count != 10_000 {
		t.Fatalf("Summary() = %+v, want max 1s and count 10000", s)
	}
}

func TestHistogramPercentileNeverExceedsMax(t *testing.T) {
	var h Histogram
	h.Add(1000 * time.Microsecond)
	s := h.Summary()
	if s.P50 != time.Millisecond || s.P99 != time.Millisecond || s.Max != time.Millisecond {
		t.Fatalf("Summary() = %+v, want every value 1ms", s)
	}
}

func TestHistogramEdgeCases(t *testing.T) {
	var h Histogram
	if got := h.Summary(); got != (Summary{}) {
		t.Fatalf("empty Summary() = %+v", got)
	}
	h.Add(-time.Second)
	if got := h.Summary(); got.Count != 1 || got.Max != 0 || got.P99 != 0 {
		t.Fatalf("negative sample Summary() = %+v, want one zero sample", got)
	}
	h.Add(3 * time.Hour)
	if got := h.Summary(); got.Max != 3*time.Hour || got.P99 != 3*time.Hour {
		t.Fatalf("Summary() = %+v, want max and p99 3h", got)
	}
	if got := h.Summary().String(); got != "n=2 p50=0s p90=3h0m0s p99=3h0m0s max=3h0m0s" {
		t.Fatalf("String() = %q", got)
	}
}

func TestBucketBoundsCoverEveryValue(t *testing.T) {
	prev := -1
	for _, us := range []uint64{0, 1, 127, 128, 129, 130, 255, 256, 1 << 20, 1<<40 + 7, 1<<62 + 1} {
		i := bucketOf(us)
		if i < prev || i >= bucketCount {
			t.Fatalf("bucketOf(%d) = %d, previous %d, count %d", us, i, prev, bucketCount)
		}
		if upper := bucketUpper(i); upper < us || (i > 0 && bucketUpper(i-1) >= us) {
			t.Fatalf("bucket %d of %d has upper %d, previous upper %d", i, us, upper, bucketUpper(i-1))
		}
		prev = i
	}
}

func TestHistogramIsSafeForConcurrentUse(t *testing.T) {
	var h Histogram
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 1000 {
				h.Add(time.Duration(w*1000+i) * time.Microsecond)
			}
		})
	}
	wg.Wait()
	if s := h.Summary(); s.Count != 8000 || s.Max != 7999*time.Microsecond {
		t.Fatalf("Summary() = %+v, want 8000 samples and max 7.999ms", s)
	}
}
