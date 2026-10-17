package publish

import "sync/atomic"

type Counters struct {
	queueFull      atomic.Int64
	malformed      atomic.Int64
	refused        atomic.Int64
	asyncFailed    atomic.Int64
	stateTrackFull atomic.Int64
}

type Drops struct {
	QueueFull      int64
	Malformed      int64
	Refused        int64
	AsyncFailed    int64
	StateTrackFull int64
}

func (c *Counters) Drops() Drops {
	return Drops{
		QueueFull:      c.queueFull.Load(),
		Malformed:      c.malformed.Load(),
		Refused:        c.refused.Load(),
		AsyncFailed:    c.asyncFailed.Load(),
		StateTrackFull: c.stateTrackFull.Load(),
	}
}

func WithCounters(c *Counters) Option {
	return func(p *Publisher) {
		if c != nil {
			p.counters = c
		}
	}
}
