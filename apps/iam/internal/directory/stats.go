package directory

import (
	"sync/atomic"
	"time"
)

type Stats struct {
	cache            *Cache
	lastLoad         atomic.Int64
	loadFailures     atomic.Uint64
	caughtUpAt       atomic.Int64
	listenUp         atomic.Bool
	stalledSince     atomic.Int64
	readErrors       atomic.Uint64
	revocationErrors atomic.Uint64
}

func NewStats(cache *Cache) *Stats {
	return &Stats{cache: cache}
}

func (s *Stats) Entries() int { return s.cache.Len() }

func (s *Stats) Ready() bool { return s.cache.Ready() }

func (s *Stats) LastLoadSeconds() float64 { return time.Duration(s.lastLoad.Load()).Seconds() }

func (s *Stats) LoadFailures() uint64 { return s.loadFailures.Load() }

func (s *Stats) LastCaughtUp() time.Time { return unixTime(s.caughtUpAt.Load()) }

func (s *Stats) ListenUp() bool { return s.listenUp.Load() }

func (s *Stats) ReadErrors() uint64 { return s.readErrors.Load() }

func (s *Stats) RevocationErrors() uint64 { return s.revocationErrors.Load() }

func (s *Stats) HorizonStalledSeconds(now time.Time) float64 {
	since := s.stalledSince.Load()
	if since == 0 {
		return 0
	}
	return max(now.Sub(unixTime(since)).Seconds(), 0)
}

func (s *Stats) markStalled(now time.Time, stalled bool) {
	switch {
	case !stalled:
		s.stalledSince.Store(0)
	case s.stalledSince.Load() == 0:
		s.stalledSince.Store(now.UnixNano())
	}
}

func unixTime(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}
