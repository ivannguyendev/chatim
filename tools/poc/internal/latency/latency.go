package latency

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

type Recorder struct {
	mu      sync.Mutex
	samples []time.Duration
}

type Summary struct {
	Count                    int
	P50, P95, P99, P999, Max time.Duration
}

func (r *Recorder) Add(d time.Duration) {
	r.mu.Lock()
	r.samples = append(r.samples, d)
	r.mu.Unlock()
}

func (r *Recorder) Summary() Summary {
	r.mu.Lock()
	s := slices.Clone(r.samples)
	r.mu.Unlock()
	return summarize(s)
}

func (r *Recorder) SummaryAndReset() Summary {
	r.mu.Lock()
	s := slices.Clone(r.samples)
	r.samples = r.samples[:0]
	r.mu.Unlock()
	return summarize(s)
}

func summarize(s []time.Duration) Summary {
	if len(s) == 0 {
		return Summary{}
	}
	slices.Sort(s)
	return Summary{Count: len(s), P50: rank(s, 500), P95: rank(s, 950), P99: rank(s, 990), P999: rank(s, 999), Max: s[len(s)-1]}
}

func (s Summary) String() string {
	return fmt.Sprintf("n=%d p50=%v p95=%v p99=%v p99.9=%v max=%v", s.Count, s.P50, s.P95, s.P99, s.P999, s.Max)
}

func rank(sorted []time.Duration, perMille int) time.Duration {
	i := (perMille*len(sorted)+999)/1000 - 1
	return sorted[max(i, 0)]
}
