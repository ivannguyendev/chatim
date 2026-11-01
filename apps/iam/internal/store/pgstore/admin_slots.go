package pgstore

import "context"

const defaultAdminDBSlots = 4

type adminSlots chan struct{}

func newAdminSlots(n int) adminSlots {
	if n <= 0 {
		n = defaultAdminDBSlots
	}
	return make(adminSlots, n)
}

func (s adminSlots) enter(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s <- struct{}{}:
		return func() { <-s }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
