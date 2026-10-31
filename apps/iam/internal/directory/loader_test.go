package directory_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func gaps(calls []time.Time) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(calls); i++ {
		out = append(out, calls[i].Sub(calls[i-1]))
	}
	return out
}

func TestLoadRetriesWithBackoffAndNeverExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.reader.set(func(f *fakeReader) {
			f.slotErrs = 8
			f.entries = []store.DirectoryEntry{entry("shop", "lan", "Lan", 1)}
		})
		if err := r.loader(nil).Run(context.Background()); err != nil {
			t.Fatalf("Run = %v; want nil", err)
		}
		var calls []time.Time
		r.reader.read(func(f *fakeReader) { calls = f.slotCalls })
		want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
		got := gaps(calls)
		if len(got) != len(want) {
			t.Fatalf("slot tries = %d; want %d", len(calls), len(want)+1)
		}
		for i, base := range want {
			base *= time.Second
			if got[i] < base*8/10 || got[i] > base*12/10 {
				t.Errorf("wait %d = %v; want %v ±20%%", i, got[i], base)
			}
		}
		if r.stats.LoadFailures() != 8 || !r.cache.Ready() || displayName(r.cache, "shop", "lan") != "Lan" {
			t.Fatalf("failures=%d ready=%v; want 8 failures and a ready cache", r.stats.LoadFailures(), r.cache.Ready())
		}
	})
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.reader.set(func(f *fakeReader) { f.slotErrs = 1 << 30 })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- r.loader(nil).Run(ctx) }()
		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("Run exited after an hour of failures: %v", err)
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel = %v; want context.Canceled", err)
		}
		if n := r.stats.LoadFailures(); n < 110 || r.cache.Ready() {
			t.Fatalf("failures=%d ready=%v; want about 120 failures and not ready", n, r.cache.Ready())
		}
	})
}

func TestLoadWaitsForASlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.reader.set(func(f *fakeReader) { f.slotBusy = 5 })
		if err := r.loader(nil).Run(context.Background()); err != nil {
			t.Fatalf("Run = %v; want nil", err)
		}
		var calls []time.Time
		r.reader.read(func(f *fakeReader) { calls = f.slotCalls })
		if len(calls) != 6 {
			t.Fatalf("slot tries = %d; want 6", len(calls))
		}
		for i, g := range gaps(calls) {
			if g < 500*time.Millisecond || g > 2*time.Second {
				t.Errorf("slot wait %d = %v; want 0.5s to 2s", i, g)
			}
		}
		if r.stats.LoadFailures() != 0 || !r.cache.Ready() {
			t.Fatalf("failures=%d ready=%v; want no failure and ready", r.stats.LoadFailures(), r.cache.Ready())
		}
	})
}

func TestLoadReleasesTheSlotOnErrorAndPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.reader.set(func(f *fakeReader) {
			f.onLoadPage = func(call int) error {
				if call == 1 {
					return errDown
				}
				panic("load page exploded")
			}
		})
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = r.loader(nil).Run(context.Background())
		}()
		if recovered != "load page exploded" {
			t.Fatalf("recovered %v; want the LoadPage panic", recovered)
		}
		var releases int
		r.reader.read(func(f *fakeReader) { releases = f.releases })
		if releases != 2 || r.stats.LoadFailures() != 1 || r.cache.Ready() {
			t.Fatalf("releases=%d failures=%d ready=%v; want 2 releases, 1 failure, not ready", releases, r.stats.LoadFailures(), r.cache.Ready())
		}
	})
}

func TestLoadCatchesUpChangesMadeDuringTheLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		var entries []store.DirectoryEntry
		for i := range 10001 {
			entries = append(entries, entry("shop", fmt.Sprintf("u%05d", i), fmt.Sprintf("User %d", i), 1))
		}
		r.reader.set(func(f *fakeReader) {
			f.entries = entries
			f.log = []store.Change{userChange("shop", "u00000", "Before start", 1)}
			f.onLoadPage = func(call int) error {
				if call == 2 {
					r.reader.add(
						userChange("shop", "u00001", "Renamed during load", 2),
						groupChange("shop", "u00002", "A group", 7),
						userChange("shop", "late", "Created during load", 1),
					)
				}
				return nil
			}
		})
		if err := r.loader(nil).Run(context.Background()); err != nil {
			t.Fatalf("Run = %v; want nil", err)
		}
		var pageCalls int
		r.reader.read(func(f *fakeReader) { pageCalls = f.loadPageCalls })
		if pageCalls != 3 {
			t.Fatalf("LoadPage calls = %d; want 3", pageCalls)
		}
		checks := map[string]string{"u00000": "User 0", "u00001": "Renamed during load", "u00002": "User 2", "late": "Created during load", "u10000": "User 10000"}
		for id, want := range checks {
			if got := displayName(r.cache, "shop", id); got != want {
				t.Errorf("%s name = %q; want %q", id, got, want)
			}
		}
		if r.cache.Len() != 10002 || r.cache.Position() != (store.Position{Xid: 4}) {
			t.Fatalf("Len=%d position=%+v; want 10002 at xid 4", r.cache.Len(), r.cache.Position())
		}
	})
}

func TestRevocationsAreLoadedBeforeReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		snap := revocation.Snapshot{Sessions: []revocation.Session{{ID: "s1", Tenant: "shop", ExpiresAt: time.Now().Add(15 * time.Minute)}}}
		var readyWhileFetching []bool
		r.revs.set(revocation.Snapshot{}, errDown)
		r.revs.onCall = func() {
			readyWhileFetching = append(readyWhileFetching, r.cache.Ready())
			r.revs.set(snap, nil)
		}
		var verdict revocation.Verdict
		var readyAtHook bool
		onLoaded := func() {
			verdict = r.list.Check(jwtverify.Claims{Tenant: "shop", Session: "s1", IssuedAt: time.Now()})
			readyAtHook = r.cache.Ready()
		}
		if err := r.loader(onLoaded).Run(context.Background()); err != nil {
			t.Fatalf("Run = %v; want nil", err)
		}
		if len(readyWhileFetching) != 2 || readyWhileFetching[0] || readyWhileFetching[1] {
			t.Fatalf("ready while fetching revocations = %v; want [false false]", readyWhileFetching)
		}
		if verdict != revocation.SessionRevoked || !readyAtHook || r.stats.LoadFailures() != 1 {
			t.Fatalf("verdict=%v ready=%v failures=%d; want revoked, ready, 1 failure", verdict, readyAtHook, r.stats.LoadFailures())
		}
	})
}

func TestOnLoadedHookRunsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.reader.set(func(f *fakeReader) {
			f.slotErrs, f.slotBusy = 2, 1
			f.onLoadPage = func(int) error {
				time.Sleep(3 * time.Second)
				return nil
			}
		})
		calls := 0
		if err := r.loader(func() { calls++ }).Run(context.Background()); err != nil {
			t.Fatalf("Run = %v; want nil", err)
		}
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("OnLoaded calls = %d; want 1", calls)
		}
		if got := r.stats.LastLoadSeconds(); got != 3 {
			t.Fatalf("LastLoadSeconds = %v; want 3", got)
		}
	})
}
