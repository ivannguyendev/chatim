package publish

import "github.com/ivannguyendev/chatim/apps/core/internal/store"

type StateClears interface {
	Add(t store.StateTarget)
}

func WithStateClears(c StateClears) Option {
	return func(p *Publisher) {
		if c != nil {
			p.clears = c
		}
	}
}
