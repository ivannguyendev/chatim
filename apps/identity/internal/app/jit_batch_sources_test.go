package app

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

type batchDocsStore struct{ Store }

func (batchDocsStore) JITLinkBatchDocs() metrics.Histogram { return metrics.Histogram{Count: 1} }

func (batchDocsStore) JITUserBatchDocs() metrics.Histogram { return metrics.Histogram{Count: 2} }

func TestJITBatchSourcesListLinksThenUsers(t *testing.T) {
	for range 20 {
		var writers []string
		var counts []uint64
		for _, s := range jitBatchSources(batchDocsStore{}) {
			writers = append(writers, s.Labels["writer"])
			counts = append(counts, s.Histogram().Count)
		}
		if !slices.Equal(writers, []string{"links", "users"}) || !slices.Equal(counts, []uint64{1, 2}) {
			t.Fatalf("jit batch sources = %v %v; want links (1) then users (2)", writers, counts)
		}
	}
}
