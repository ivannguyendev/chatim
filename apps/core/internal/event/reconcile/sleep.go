package reconcile

import (
	"context"
	"time"
)

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
