package resilience

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

var ErrOverloaded = errors.New("resilience: overloaded")

type Limiter struct {
	slots     chan struct{}
	queueWait time.Duration
	rejected  atomic.Int64
}

func NewLimiter(maxInFlight int, queueWait time.Duration) *Limiter {
	return &Limiter{
		slots:     make(chan struct{}, max(maxInFlight, 1)),
		queueWait: max(queueWait, 0),
	}
}

func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.slots <- struct{}{}:
		return nil
	default:
	}
	if l.queueWait == 0 {
		l.rejected.Add(1)
		return ErrOverloaded
	}

	t := time.NewTimer(l.queueWait)
	defer t.Stop()
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-t.C:
		l.rejected.Add(1)
		return ErrOverloaded
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) Release() {
	select {
	case <-l.slots:
	default:
		panic("resilience: Release without a matching Acquire")
	}
}

func (l *Limiter) InFlight() int { return len(l.slots) }

func (l *Limiter) Capacity() int { return cap(l.slots) }

func (l *Limiter) Rejected() int64 { return l.rejected.Load() }
