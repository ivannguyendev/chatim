package limit

import (
	"errors"
	"sync/atomic"
)

type Gate struct {
	max int64
	n   atomic.Int64
}

func NewGate(size int) (*Gate, error) {
	if size <= 0 {
		return nil, errors.New("limit: gate size must be positive")
	}
	return &Gate{max: int64(size)}, nil
}

func (g *Gate) Enter() bool {
	if g.n.Add(1) > g.max {
		g.n.Add(-1)
		return false
	}
	return true
}

func (g *Gate) Leave() { g.n.Add(-1) }

func (g *Gate) InFlight() int { return int(g.n.Load()) }
