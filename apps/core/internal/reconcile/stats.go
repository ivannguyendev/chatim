package reconcile

import "sync/atomic"

type Stats struct {
	Running     bool
	Terms       uint64
	Forwarded   uint64
	Dropped     uint64
	HistoryLost uint64
}

type counters struct {
	running     atomic.Bool
	terms       atomic.Uint64
	forwarded   atomic.Uint64
	dropped     atomic.Uint64
	historyLost atomic.Uint64
}

func (r *Reconciler) Stats() Stats {
	return Stats{
		Running:     r.stats.running.Load(),
		Terms:       r.stats.terms.Load(),
		Forwarded:   r.stats.forwarded.Load(),
		Dropped:     r.stats.dropped.Load(),
		HistoryLost: r.stats.historyLost.Load(),
	}
}

func (r *Reconciler) termStarted() {
	r.stats.terms.Add(1)
	r.stats.running.Store(true)
}

func (r *Reconciler) termEnded() { r.stats.running.Store(false) }
