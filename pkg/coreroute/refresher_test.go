package coreroute

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestThousandConcurrentTriggersWhileRedisFailsLoadAtMostTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var loads atomic.Int32
		r := newRefresher(func(ctx context.Context) error {
			loads.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return errors.New("redis down")
		}, slog.New(slog.DiscardHandler))
		ctx, cancel := context.WithCancel(t.Context())
		go r.run(ctx)
		var wg sync.WaitGroup
		for range 1000 {
			wg.Go(r.trigger)
		}
		wg.Wait()
		synctest.Wait()
		close(release)
		synctest.Wait()
		if n := loads.Load(); n < 1 || n > 2 {
			t.Fatalf("1000 triggers caused %d loads, want 1 or 2", n)
		}
		cancel()
		r.wait()
		r.trigger()
	})
}

func TestRefresherLoadsAgainForATriggerAfterTheLastLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var loads atomic.Int32
		r := newRefresher(func(context.Context) error { loads.Add(1); return nil }, slog.New(slog.DiscardHandler))
		ctx, cancel := context.WithCancel(t.Context())
		go r.run(ctx)
		for want := int32(1); want <= 3; want++ {
			r.trigger()
			synctest.Wait()
			if n := loads.Load(); n != want {
				t.Fatalf("loads = %d after trigger %d, want %d", n, want, want)
			}
		}
		cancel()
		r.wait()
	})
}
