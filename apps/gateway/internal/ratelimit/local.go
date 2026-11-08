package ratelimit

import "sync"

type window struct {
	index int64
	count int64
}

type local struct {
	mu       sync.Mutex
	counts   map[string]window
	capacity int
}

func newLocal(capacity int) *local {
	return &local{counts: map[string]window{}, capacity: capacity}
}

func (l *local) count(keys []string, indexes []int64) []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.counts)+len(keys) > l.capacity {
		l.evict(keys, indexes)
	}
	out := make([]int64, len(keys))
	for i, k := range keys {
		w := l.counts[k]
		if w.index != indexes[i] {
			w = window{index: indexes[i]}
		}
		w.count++
		l.counts[k] = w
		out[i] = w.count
	}
	return out
}

func (l *local) evict(keys []string, indexes []int64) {
	current := make(map[string]int64, len(keys))
	for i, k := range keys {
		current[k] = indexes[i]
	}
	for k, w := range l.counts {
		if idx, ok := current[k]; !ok || w.index != idx {
			delete(l.counts, k)
		}
	}
	if len(l.counts)+len(keys) > l.capacity {
		clear(l.counts)
	}
}
