package wsload

import (
	"fmt"
	"math/bits"
	"sync/atomic"
	"time"
)

const (
	subBits     = 6
	linearLimit = 1 << (subBits + 1)
	bucketCount = (65 - subBits) << subBits
)

type Histogram struct {
	counts [bucketCount]atomic.Uint64
	total  atomic.Uint64
	max    atomic.Int64
}

type Summary struct {
	Count              uint64
	P50, P90, P99, Max time.Duration
}

func (h *Histogram) Add(d time.Duration) {
	d = max(d, 0)
	h.counts[bucketOf(uint64(d.Microseconds()))].Add(1)
	h.total.Add(1)
	for {
		old := h.max.Load()
		if int64(d) <= old || h.max.CompareAndSwap(old, int64(d)) {
			return
		}
	}
}

func (h *Histogram) Summary() Summary {
	var counts [bucketCount]uint64
	var total uint64
	for i := range counts {
		counts[i] = h.counts[i].Load()
		total += counts[i]
	}
	if total == 0 {
		return Summary{}
	}
	top := time.Duration(h.max.Load())
	at := func(perMille uint64) time.Duration {
		return min(percentile(&counts, total, perMille), top)
	}
	return Summary{Count: total, P50: at(500), P90: at(900), P99: at(990), Max: top}
}

func (s Summary) String() string {
	return fmt.Sprintf("n=%d p50=%v p90=%v p99=%v max=%v", s.Count, s.P50, s.P90, s.P99, s.Max)
}

func percentile(counts *[bucketCount]uint64, total, perMille uint64) time.Duration {
	rank := max((perMille*total+999)/1000, 1)
	var seen uint64
	for i, n := range counts {
		if seen += n; seen >= rank {
			return time.Duration(bucketUpper(i)) * time.Microsecond
		}
	}
	return 0
}

func bucketOf(us uint64) int {
	if us < linearLimit {
		return int(us)
	}
	shift := bits.Len64(us) - (subBits + 1)
	return shift<<subBits + int(us>>shift)
}

func bucketUpper(i int) uint64 {
	if i < linearLimit {
		return uint64(i)
	}
	shift := i>>subBits - 1
	mantissa := uint64(i - shift<<subBits)
	return (mantissa+1)<<shift - 1
}
