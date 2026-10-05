package reconcile

import (
	"sync/atomic"
	"time"
)

type Stats struct {
	Running     bool
	Terms       uint64
	Republished uint64
	Dropped     uint64
	HistoryLost uint64
	Lag         time.Duration
}

type counters struct {
	running     atomic.Bool
	terms       atomic.Uint64
	republished atomic.Uint64
	historyLost atomic.Uint64
	lag         atomic.Int64
}

func (r *Reconciler) Stats() Stats {
	return Stats{
		Running:     r.stats.running.Load(),
		Terms:       r.stats.terms.Load(),
		Republished: r.stats.republished.Load(),
		Dropped:     r.dropped.Load(),
		HistoryLost: r.stats.historyLost.Load(),
		Lag:         time.Duration(r.stats.lag.Load()),
	}
}

func (r *Reconciler) termStarted() {
	r.stats.terms.Add(1)
	r.stats.running.Store(true)
}

func (r *Reconciler) termEnded() {
	r.stats.running.Store(false)
	r.stats.lag.Store(0)
}
