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
	readErrors       atomic.Uint64
	revocationErrors atomic.Uint64
	streamUp         atomic.Bool
	streamReloads    atomic.Uint64
}

func NewStats(cache *Cache) *Stats {
	return &Stats{cache: cache}
}

func (s *Stats) Entries() int { return s.cache.Len() }

func (s *Stats) Ready() bool { return s.cache.Ready() }

func (s *Stats) LastLoadSeconds() float64 { return time.Duration(s.lastLoad.Load()).Seconds() }

func (s *Stats) LoadFailures() uint64 { return s.loadFailures.Load() }

func (s *Stats) LastCaughtUp() time.Time { return unixTime(s.caughtUpAt.Load()) }

func (s *Stats) ReadErrors() uint64 { return s.readErrors.Load() }

func (s *Stats) RevocationErrors() uint64 { return s.revocationErrors.Load() }

func (s *Stats) StreamUp() bool { return s.streamUp.Load() }

func (s *Stats) StreamReloads() uint64 { return s.streamReloads.Load() }

func unixTime(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}
