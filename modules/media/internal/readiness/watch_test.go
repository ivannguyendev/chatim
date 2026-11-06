package readiness_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/modules/media/internal/readiness"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestReadyFollowsTheBucketCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var failing atomic.Bool
		failing.Store(true)
		checks := atomic.Int64{}
		w := readiness.New(func(context.Context) error {
			checks.Add(1)
			if failing.Load() {
				return errors.New("bucket missing")
			}
			return nil
		}, 30*time.Second, slog.New(slog.DiscardHandler))
		if err := w.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		synctest.Wait()
		if w.Ready() || checks.Load() != 1 {
			t.Fatalf("ready %v after %d checks", w.Ready(), checks.Load())
		}
		failing.Store(false)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if !w.Ready() || checks.Load() != 2 {
			t.Fatalf("ready %v after %d checks", w.Ready(), checks.Load())
		}
		failing.Store(true)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if w.Ready() {
			t.Fatal("still ready after storage went away")
		}
	})
}

func TestCheckIsBoundedAndStopEndsTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := readiness.New(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, time.Minute, slog.New(slog.DiscardHandler))
		if err := w.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if w.Ready() {
			t.Fatal("a hung check made storage ready")
		}
		w.Stop()
		w.Stop()
	})
}
