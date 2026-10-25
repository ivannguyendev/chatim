package coreroute

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

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
			time.Sleep(minReloadGap)
		}
		cancel()
		r.wait()
	})
}

func TestTriggersSpreadOverTimeReloadAtMostOncePerGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var loads []time.Time
		r := newRefresher(func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			loads = append(loads, time.Now())
			return errors.New("redis down")
		}, slog.New(slog.DiscardHandler))
		ctx, cancel := context.WithCancel(t.Context())
		go r.run(ctx)
		const window = 10 * minReloadGap
		for elapsed := time.Duration(0); elapsed < window; elapsed += 10 * time.Millisecond {
			r.trigger()
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		r.wait()
		if n := len(loads); n < 9 || n > 11 {
			t.Fatalf("triggers every 10ms for %v caused %d loads, want about one per %v", window, n, minReloadGap)
		}
		for i := 1; i < len(loads); i++ {
			if gap := loads[i].Sub(loads[i-1]); gap < minReloadGap {
				t.Fatalf("loads %d and %d only %v apart, want at least %v", i-1, i, gap, minReloadGap)
			}
		}
	})
}

func TestStopDuringTheReloadGapReturnsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRefresher(func(context.Context) error { return nil }, slog.New(slog.DiscardHandler))
		ctx, cancel := context.WithCancel(t.Context())
		go r.run(ctx)
		r.trigger()
		synctest.Wait()
		start := time.Now()
		cancel()
		r.wait()
		if took := time.Since(start); took != 0 {
			t.Fatalf("stop during the gap took %v, want at once", took)
		}
	})
}
