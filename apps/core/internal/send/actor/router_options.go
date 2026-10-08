package actor

import "github.com/ivannguyendev/chatim/apps/core/internal/model/access"

type Option func(*Router)

func WithPolicy(p access.Policy) Option {
	return func(r *Router) {
		if p != nil {
			r.policy = p
		}
	}
}
