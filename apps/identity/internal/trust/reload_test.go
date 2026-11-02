package trust_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

type running struct {
	cancel context.CancelFunc
	done   chan error
}

func startReloader(r *trust.Reloader) running {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	return running{cancel: cancel, done: done}
}

func (r running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	if err := <-r.done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v; want context.Canceled", err)
	}
}

func liveToken(t *testing.T) string {
	t.Helper()
	return signEd(t, claimsAt(time.Now()))
}

func TestReloaderLoadsAtStartAndDropsARemovedKeyAfterOnePeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		v := newVerifier(t, nil)
		run := startReloader(trust.NewReloader(v, src, 10*time.Second, nil))
		defer run.stop(t)
		synctest.Wait()
		if !v.Ready() {
			t.Fatal("not ready after the first load")
		}
		if _, err := v.Verify(liveToken(t)); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		src.removeKey(acmeIssuer, "ed1")
		time.Sleep(10*time.Second - time.Millisecond)
		synctest.Wait()
		if _, err := v.Verify(liveToken(t)); err != nil {
			t.Fatalf("removed key refused before the period: %v", err)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		_, err := v.Verify(liveToken(t))
		if trust.ReasonOf(err) != trust.ReasonKey {
			t.Fatalf("after one period reason = %q; want key", trust.ReasonOf(err))
		}
	})
}

func TestReloaderWakeReloadsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		v := newVerifier(t, nil)
		r := trust.NewReloader(v, src, 10*time.Second, nil)
		run := startReloader(r)
		defer run.stop(t)
		synctest.Wait()
		began := time.Now()
		src.setTenantState("acme", store.StateDisabled)
		r.Wake()
		synctest.Wait()
		_, err := v.Verify(liveToken(t))
		if trust.ReasonOf(err) != trust.ReasonTenantDisabled || time.Since(began) != 0 {
			t.Fatalf("reason = %q after %v; want tenant_disabled at once", trust.ReasonOf(err), time.Since(began))
		}
		if calls := src.loadCalls(); calls != 2 {
			t.Fatalf("loads = %d; want 2", calls)
		}
	})
}

func TestReloaderWakeNeverBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		r := trust.NewReloader(newVerifier(t, nil), src, 10*time.Second, nil)
		for range 5 {
			r.Wake()
		}
		run := startReloader(r)
		defer run.stop(t)
		synctest.Wait()
		if calls := src.loadCalls(); calls != 2 {
			t.Fatalf("loads = %d; want 2 (start plus one pending wake)", calls)
		}
	})
}

func TestReloaderKeepsTryingUntilTheSourceAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newSource()
		src.update(func(f *fakeSource) { f.err = errSource })
		v := newVerifier(t, nil)
		run := startReloader(trust.NewReloader(v, src, 10*time.Second, nil))
		defer run.stop(t)
		synctest.Wait()
		if v.Ready() || v.Stats().ReloadErrors() != 1 {
			t.Fatalf("ready %v reload errors %d; want false and 1", v.Ready(), v.Stats().ReloadErrors())
		}
		src.update(func(f *fakeSource) { f.err = nil })
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if !v.Ready() || v.Stats().ReloadErrors() != 1 {
			t.Fatalf("ready %v reload errors %d; want true and 1", v.Ready(), v.Stats().ReloadErrors())
		}
	})
}

func TestReloaderBoundsALoadByThePeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &blockingSource{}
		v := newVerifier(t, nil)
		run := startReloader(trust.NewReloader(v, src, 10*time.Second, nil))
		defer run.stop(t)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if v.Stats().ReloadErrors() != 1 {
			t.Fatalf("reload errors = %d; want 1 after a load hung for one period", v.Stats().ReloadErrors())
		}
	})
}

type blockingSource struct{}

func (blockingSource) TrustedIssuers(ctx context.Context) ([]store.TrustedIssuer, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingSource) TrustedKeys(ctx context.Context) ([]store.TrustedKey, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
