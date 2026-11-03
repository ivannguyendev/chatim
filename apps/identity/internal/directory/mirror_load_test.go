package directory_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
)

func TestMirrorWaitsForAFreeSlotBeforeLoading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, entry("shop", "lan", "Lan", 1))
		r.src.set(func(f *fakeSource) { f.slotBusy = 4 })
		r.start(t)
		synctest.Wait()
		if r.cache.Ready() || r.stats.LoadFailures() != 0 {
			t.Fatalf("ready=%v failures=%d with no free slot; want not ready and no failure", r.cache.Ready(), r.stats.LoadFailures())
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		var calls []time.Time
		r.src.read(func(f *fakeSource) { calls = slices.Clone(f.slotCalls) })
		if len(calls) != 5 {
			t.Fatalf("slot tries = %d; want 5", len(calls))
		}
		for i, gap := range gaps(calls) {
			if gap < 500*time.Millisecond || gap > 2*time.Second {
				t.Errorf("wait %d = %v; want 0.5s to 2s", i, gap)
			}
		}
		if !r.cache.Ready() || displayName(r.cache, "shop", "lan") != "Lan" || r.stats.LoadFailures() != 0 {
			t.Fatalf("ready=%v failures=%d after the slot freed", r.cache.Ready(), r.stats.LoadFailures())
		}
	})
}

func TestMirrorLoadsPagesIntoANewMapAndSwapsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, manyEntries(12000)...)
		var seenMu sync.Mutex
		var seenDuringLoad []int
		r.src.set(func(f *fakeSource) {
			f.pageHook = func(int) error {
				seenMu.Lock()
				defer seenMu.Unlock()
				if r.cache.Ready() {
					seenDuringLoad = append(seenDuringLoad, -1)
				}
				seenDuringLoad = append(seenDuringLoad, r.cache.Len())
				return nil
			}
		})
		r.start(t)
		synctest.Wait()
		seenMu.Lock()
		defer seenMu.Unlock()
		if !slices.Equal(seenDuringLoad, []int{0, 0, 0}) {
			t.Fatalf("cache seen by the pages = %v; want empty and not ready on all 3", seenDuringLoad)
		}
		var limits []int
		r.src.read(func(f *fakeSource) { limits = slices.Clone(f.pageLimits) })
		if !slices.Equal(limits, []int{5000, 5000, 5000}) {
			t.Fatalf("page limits = %v; want three pages of 5000", limits)
		}
		if !r.cache.Ready() || r.cache.Len() != 12000 || r.loaded.Load() != 1 || string(r.cache.Token()) != "T0" {
			t.Fatalf("ready=%v len=%d loaded=%d token=%q; want ready, 12000, one OnLoaded, T0",
				r.cache.Ready(), r.cache.Len(), r.loaded.Load(), r.cache.Token())
		}
		if _, releases := r.src.slot(t, 0).counts(); releases != 1 {
			t.Fatalf("slot releases = %d; want 1", releases)
		}
		if s := r.src.stream(t, 0); s.from != "T0" {
			t.Fatalf("stream opened from %q; want T0", s.from)
		}
		if r.src.count() != 1 || !r.stats.StreamUp() {
			t.Fatalf("revocation loads=%d stream up=%v; want 1 and up", r.src.count(), r.stats.StreamUp())
		}
	})
}

func TestMirrorLoadErrorBacksOffAndReleasesTheSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, manyEntries(12000)...)
		r.src.set(func(f *fakeSource) {
			f.pageHook = func(call int) error {
				if call == 2 {
					return errMidPage
				}
				return nil
			}
		})
		r.start(t)
		synctest.Wait()
		if r.cache.Ready() || r.stats.LoadFailures() != 1 || r.cache.Len() != 0 {
			t.Fatalf("ready=%v failures=%d len=%d after a failed page; want not ready, 1, 0", r.cache.Ready(), r.stats.LoadFailures(), r.cache.Len())
		}
		if _, releases := r.src.slot(t, 0).counts(); releases != 1 {
			t.Fatalf("failed pass slot releases = %d; want 1", releases)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		var calls []time.Time
		r.src.read(func(f *fakeSource) { calls = slices.Clone(f.slotCalls) })
		if len(calls) != 2 || calls[1].Sub(calls[0]) < 800*time.Millisecond || calls[1].Sub(calls[0]) > 1200*time.Millisecond {
			t.Fatalf("slot tries %v; want a retry after 1s ±20%%", gaps(calls))
		}
		if _, releases := r.src.slot(t, 1).counts(); releases != 1 || !r.cache.Ready() || r.cache.Len() != 12000 {
			t.Fatalf("retry releases=%d ready=%v len=%d", releases, r.cache.Ready(), r.cache.Len())
		}
	})
}

func TestMirrorRenewsTheSlotDuringALongLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, manyEntries(12000)...)
		r.src.set(func(f *fakeSource) {
			f.pageHook = func(int) error { time.Sleep(6 * time.Second); return nil }
		})
		began := time.Now()
		r.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()
		slot := r.src.slot(t, 0)
		slot.mu.Lock()
		renews := slices.Clone(slot.renews)
		slot.mu.Unlock()
		if len(renews) != 1 || renews[0].Sub(began) != 12*time.Second {
			t.Fatalf("renews = %v; want one at 12s", renews)
		}
		if !r.cache.Ready() || r.cache.Len() != 12000 {
			t.Fatalf("ready=%v len=%d after a long load", r.cache.Ready(), r.cache.Len())
		}
	})
}

func TestMirrorAbortsTheLoadWhenTheSlotIsLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, manyEntries(12000)...)
		r.src.set(func(f *fakeSource) {
			f.slotLost = 1
			f.pageHook = func(int) error { time.Sleep(6 * time.Second); return nil }
		})
		r.start(t)
		time.Sleep(12*time.Second + 500*time.Millisecond)
		synctest.Wait()
		renews, releases := r.src.slot(t, 0).counts()
		var pages int
		r.src.read(func(f *fakeSource) { pages = f.pageCalls })
		if renews != 1 || releases != 1 || pages != 2 || r.stats.LoadFailures() != 1 || r.cache.Ready() {
			t.Fatalf("renews=%d releases=%d pages=%d failures=%d ready=%v; want the pass aborted after page 2",
				renews, releases, pages, r.stats.LoadFailures(), r.cache.Ready())
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if !r.cache.Ready() || r.cache.Len() != 12000 {
			t.Fatalf("ready=%v len=%d after the retry", r.cache.Ready(), r.cache.Len())
		}
	})
}

func TestMirrorReloadsRevocationsWhileWaitingForASlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t)
		r.recheck = 5 * time.Second
		r.src.set(func(f *fakeSource) { f.slotBusy = 1 << 20 })
		r.start(t)
		time.Sleep(16 * time.Second)
		synctest.Wait()
		if n := r.src.count(); n != 3 {
			t.Fatalf("revocation loads after 16s = %d; want 3 at 5s, 10s and 15s", n)
		}
		r.src.set(func(f *fakeSource) { f.slotBusy, f.slotErrs = 0, 1<<20 })
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if n := r.src.count(); n < 6 || r.cache.Ready() {
			t.Fatalf("revocation loads while loads fail = %d ready=%v; want at least 6 and not ready", n, r.cache.Ready())
		}
	})
}

func TestMirrorReleasesTheSlotWhenStoppedMidLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, manyEntries(12000)...)
		r.src.set(func(f *fakeSource) {
			f.pageHook = func(int) error { time.Sleep(time.Hour); return nil }
		})
		r.start(t)
		synctest.Wait()
		r.stop(t)
		if _, releases := r.src.slot(t, 0).counts(); releases != 1 || r.cache.Ready() || r.stats.LoadFailures() != 0 {
			t.Fatalf("releases=%d ready=%v failures=%d after stop mid load", releases, r.cache.Ready(), r.stats.LoadFailures())
		}
	})
}

func TestMirrorNeedsItsParts(t *testing.T) {
	r := newMirrorRig(t)
	for name, mod := range map[string]func(m *directory.Mirror){
		"source":  func(m *directory.Mirror) { m.Source = nil },
		"cache":   func(m *directory.Mirror) { m.Cache = nil },
		"list":    func(m *directory.Mirror) { m.List = nil },
		"stats":   func(m *directory.Mirror) { m.Stats = nil },
		"slots":   func(m *directory.Mirror) { m.Slots = 0 },
		"recheck": func(m *directory.Mirror) { m.Recheck = 0 },
	} {
		m := r.mirror()
		mod(m)
		if err := m.Run(context.Background()); err == nil {
			t.Errorf("Run without %s = nil; want an error", name)
		}
	}
}
