package publish

import "github.com/ivannguyendev/chatim/apps/core/internal/store"

type StateClears interface {
	AddAll(targets []store.StateTarget)
}

func WithStateClears(c StateClears) Option {
	return func(p *Publisher) {
		if c != nil {
			p.clears = c
		}
	}
}
