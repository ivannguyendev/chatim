package roomcache

import "sync/atomic"

type stats struct {
	hits   atomic.Uint64
	misses atomic.Uint64
}

func (s *stats) Hits() uint64 { return s.hits.Load() }

func (s *stats) Misses() uint64 { return s.misses.Load() }

func (s *stats) count(hit bool) {
	if hit {
		s.hits.Add(1)
		return
	}
	s.misses.Add(1)
}
