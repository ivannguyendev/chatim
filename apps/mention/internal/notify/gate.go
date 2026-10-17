package notify

import "time"

type Gate struct {
	maxAge time.Duration
	stats  *Stats
}

func NewGate(maxAge time.Duration, stats *Stats) *Gate {
	return &Gate{maxAge: maxAge, stats: stats}
}

func (g *Gate) Fresh(ts time.Time) bool {
	if time.Since(ts) > g.maxAge {
		g.stats.stale.Add(1)
		return false
	}
	return true
}

func (g *Gate) Latest(eventVer, currentVer uint32) bool {
	if eventVer < currentVer {
		g.stats.superseded.Add(1)
		return false
	}
	return true
}
