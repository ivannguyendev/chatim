package effects

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type recordGroup[K comparable] struct {
	key     K
	indexes []int
}

func groupRecords[K comparable](recs []work.Record, keyOf func(work.Record) K) []recordGroup[K] {
	at := make(map[K]int, len(recs))
	var out []recordGroup[K]
	for i, r := range recs {
		k := keyOf(r)
		j, ok := at[k]
		if !ok {
			j = len(out)
			at[k] = j
			out = append(out, recordGroup[K]{key: k})
		}
		out[j].indexes = append(out[j].indexes, i)
	}
	return out
}

func (g recordGroup[K]) fail(errs []error, err error) {
	for _, i := range g.indexes {
		errs[i] = err
	}
}

func (g recordGroup[K]) share(errs []error) {
	for _, i := range g.indexes[1:] {
		errs[i] = errs[g.indexes[0]]
	}
}

func (g recordGroup[K]) drop(n *atomic.Uint64) {
	for range g.indexes {
		n.Add(1)
	}
}

func recordRoom(r work.Record) uint64 { return r.Room }
