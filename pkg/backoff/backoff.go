package backoff

import (
	"context"
	"math/rand/v2"
	"time"
)

func Jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1)
}

func Pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
