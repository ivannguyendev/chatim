package sweep

import (
	"context"
	"time"
)

type pace struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newPace(perSecond int) *pace {
	r := float64(perSecond)
	return &pace{rate: r, burst: r, tokens: r}
}

func (p *pace) take(ctx context.Context, stop <-chan struct{}, n int) error {
	if n <= 0 {
		return nil
	}
	now := time.Now()
	if !p.last.IsZero() {
		p.tokens = min(p.burst, p.tokens+now.Sub(p.last).Seconds()*p.rate)
	}
	p.last = now
	p.tokens -= float64(n)
	if p.tokens >= 0 {
		return nil
	}
	return sleep(ctx, stop, time.Duration(-p.tokens/p.rate*float64(time.Second)))
}

func sleep(ctx context.Context, stop <-chan struct{}, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-stop:
		return errStopped
	case <-ctx.Done():
		return ctx.Err()
	}
}
