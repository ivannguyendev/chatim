package mongostore

import (
	"sort"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

var jitBatchBounds = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512}

type docsHistogram struct {
	bounds  []float64
	buckets []atomic.Uint64
	count   atomic.Uint64
	sum     atomic.Int64
}

func newDocsHistogram(bounds []float64) *docsHistogram {
	return &docsHistogram{bounds: bounds, buckets: make([]atomic.Uint64, len(bounds))}
}

func (h *docsHistogram) observe(docs int) {
	if i := sort.SearchFloat64s(h.bounds, float64(docs)); i < len(h.bounds) {
		h.buckets[i].Add(1)
	}
	h.sum.Add(int64(docs))
	h.count.Add(1)
}

func (h *docsHistogram) read() metrics.Histogram {
	out := metrics.Histogram{Bounds: h.bounds, Cumulative: make([]uint64, len(h.bounds))}
	var running uint64
	for i := range h.bounds {
		running += h.buckets[i].Load()
		out.Cumulative[i] = running
	}
	out.Count = max(h.count.Load(), running)
	out.Sum = float64(h.sum.Load())
	return out
}

func (s *Store) JITLinkBatchDocs() metrics.Histogram { return s.links.sizes.read() }

func (s *Store) JITUserBatchDocs() metrics.Histogram { return s.users.sizes.read() }
