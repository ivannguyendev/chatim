package notify

import "sync/atomic"

const (
	ReasonStale      = "stale"
	ReasonSuperseded = "superseded"
)

type Stats struct {
	notified   atomic.Uint64
	retracted  atomic.Uint64
	failures   atomic.Uint64
	stale      atomic.Uint64
	superseded atomic.Uint64
}

func (s *Stats) Notified() uint64 { return s.notified.Load() }

func (s *Stats) Retracted() uint64 { return s.retracted.Load() }

func (s *Stats) Failures() uint64 { return s.failures.Load() }

func (s *Stats) Skipped(reason string) uint64 {
	switch reason {
	case ReasonStale:
		return s.stale.Load()
	case ReasonSuperseded:
		return s.superseded.Load()
	default:
		return 0
	}
}
