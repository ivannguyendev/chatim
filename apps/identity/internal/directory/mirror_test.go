package directory_test

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/auth"
)

func startedMirror(t *testing.T, entries ...store.DirectoryEntry) *mirrorRig {
	t.Helper()
	r := newMirrorRig(t, entries...)
	r.start(t)
	synctest.Wait()
	if !r.cache.Ready() {
		t.Fatal("mirror not ready after the load")
	}
	return r
}

func TestMirrorAppliesUserEventsWithTheVerFilter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		s := r.src.stream(t, 0)
		s.send(userEvent(entry("shop", "lan", "Lan Anh", 2), "T1"))
		s.send(userEvent(entry("shop", "mai", "Mai", 1), "T2"))
		s.send(userEvent(entry("shop", "lan", "Old Lan", 1), "T3"))
		odd := entry("shop", "odd", "Odd", 1)
		odd.State = 9
		s.send(userEvent(odd, "T4"))
		synctest.Wait()
		if displayName(r.cache, "shop", "lan") != "Lan Anh" || displayName(r.cache, "shop", "mai") != "Mai" || r.cache.Len() != 2 {
			t.Fatalf("lan=%q mai=%q len=%d; want the newer lan, mai and no odd state",
				displayName(r.cache, "shop", "lan"), displayName(r.cache, "shop", "mai"), r.cache.Len())
		}
		if string(r.cache.Token()) != "T4" {
			t.Fatalf("token = %q; want T4 even after dropped events", r.cache.Token())
		}
	})
}

func TestMirrorWakesTrustAndReloadsRevocationsOnTheirEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		s := r.src.stream(t, 0)
		s.send(kindEvent("T1", store.EventTrust))
		synctest.Wait()
		if r.trusted.Load() != 1 || r.src.count() != 1 {
			t.Fatalf("trust wakes=%d revocation loads=%d; want 1 and 1", r.trusted.Load(), r.src.count())
		}
		r.src.fakeRevocations.set(auth.RevocationSnapshot{Sessions: []auth.RevokedSession{{ID: "iphone", Tenant: "shop", ExpiresAt: time.Now().Add(time.Hour)}}}, nil)
		s.send(kindEvent("T2", store.EventRevocation))
		synctest.Wait()
		if sessions, _ := r.list.Counts(); r.src.count() != 2 || sessions != 1 || r.trusted.Load() != 1 {
			t.Fatalf("revocation loads=%d sessions=%d trust wakes=%d; want 2, 1, 1", r.src.count(), sessions, r.trusted.Load())
		}
		s.send(kindEvent("T3", store.EventTrust, store.EventRevocation))
		synctest.Wait()
		if r.trusted.Load() != 2 || r.src.count() != 3 || string(r.cache.Token()) != "T3" {
			t.Fatalf("trust wakes=%d revocation loads=%d token=%q; want 2, 3, T3", r.trusted.Load(), r.src.count(), r.cache.Token())
		}
	})
}

func TestMirrorIdleResultAdvancesTheTokenAndCatchesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		if !r.stats.LastCaughtUp().IsZero() {
			t.Fatalf("caught up at %v before any stream result; want zero", r.stats.LastCaughtUp())
		}
		time.Sleep(500 * time.Millisecond)
		r.src.stream(t, 0).send(streamResult{ev: store.StreamEvent{Token: store.StreamToken("T9")}})
		synctest.Wait()
		if string(r.cache.Token()) != "T9" || !r.stats.LastCaughtUp().Equal(time.Now()) {
			t.Fatalf("token=%q caught up=%v now=%v; want T9 caught up now", r.cache.Token(), r.stats.LastCaughtUp(), time.Now())
		}
	})
}

func TestMirrorLagStaysSmallUnderContinuousWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		s := r.src.stream(t, 0)
		for i := range 600 {
			time.Sleep(100 * time.Millisecond)
			ev := userEvent(entry("shop", fmt.Sprintf("w%04d", i), "Writer", 1), fmt.Sprintf("W%d", i))
			ev.ev.CaughtUp = i%10 == 9
			s.send(ev)
		}
		synctest.Wait()
		if lag := time.Since(r.stats.LastCaughtUp()); lag > time.Second {
			t.Fatalf("lag after 60s of full batches = %v; want under 1s", lag)
		}
		caught := r.stats.LastCaughtUp()
		time.Sleep(500 * time.Millisecond)
		s.send(userEvent(entry("shop", "mid", "Mid batch", 1), "M"))
		synctest.Wait()
		if !r.stats.LastCaughtUp().Equal(caught) || r.cache.Len() != 602 {
			t.Fatalf("mid batch event moved caught up from %v to %v (len %d); want unchanged", caught, r.stats.LastCaughtUp(), r.cache.Len())
		}
	})
}

func TestMirrorReopensFromTheLastTokenAfterAStreamError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		s := r.src.stream(t, 0)
		if !r.stats.StreamUp() {
			t.Fatal("stream not up while following")
		}
		s.send(userEvent(entry("shop", "lan", "Lan Anh", 2), "T1"))
		r.src.set(func(f *fakeSource) { f.openErrs = []error{errDown} })
		s.send(streamResult{err: errDown})
		synctest.Wait()
		if r.stats.StreamUp() || r.stats.ReadErrors() != 1 || s.closed.Load() != 1 || !r.cache.Ready() {
			t.Fatalf("up=%v read errors=%d closed=%d ready=%v after a stream error", r.stats.StreamUp(), r.stats.ReadErrors(), s.closed.Load(), r.cache.Ready())
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		var opens []time.Time
		r.src.read(func(f *fakeSource) { opens = slices.Clone(f.opens) })
		got := gaps(opens)
		if len(got) != 2 || got[0] < 800*time.Millisecond || got[0] > 1200*time.Millisecond || got[1] < 1600*time.Millisecond || got[1] > 2400*time.Millisecond {
			t.Fatalf("reopen waits = %v; want 1s then 2s ±20%%", got)
		}
		if from := r.src.stream(t, 1).from; from != "T1" || !r.stats.StreamUp() || r.stats.ReadErrors() != 2 || r.stats.StreamReloads() != 0 {
			t.Fatalf("reopened from %q up=%v read errors=%d reloads=%d; want T1, up, 2, 0", from, r.stats.StreamUp(), r.stats.ReadErrors(), r.stats.StreamReloads())
		}
	})
}

func TestMirrorReloadsWhenTheStreamPositionIsLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		gate := make(chan struct{})
		r.src.set(func(f *fakeSource) {
			f.entries = []store.DirectoryEntry{entry("shop", "mai", "Mai", 1)}
			f.pageHook = func(call int) error {
				if call >= 2 {
					<-gate
				}
				return nil
			}
		})
		s := r.src.stream(t, 0)
		s.send(streamResult{err: fmt.Errorf("%w: history gone", store.ErrStreamLost)})
		synctest.Wait()
		var slotCalls int
		r.src.read(func(f *fakeSource) { slotCalls = len(f.slotCalls) })
		if slotCalls != 1 || !r.cache.Ready() || len(r.cache.Token()) != 0 {
			t.Fatalf("slots=%d ready=%v token=%q right after a lost position; want no reload before the pause", slotCalls, r.cache.Ready(), r.cache.Token())
		}
		time.Sleep(1200 * time.Millisecond)
		synctest.Wait()
		var starts int
		r.src.read(func(f *fakeSource) { slotCalls, starts = len(f.slotCalls), f.starts })
		if r.stats.StreamReloads() != 1 || r.stats.ReadErrors() != 0 || r.stats.StreamUp() || s.closed.Load() != 1 || slotCalls != 2 || starts != 2 {
			t.Fatalf("reloads=%d read errors=%d up=%v closed=%d slots=%d starts=%d after a lost position",
				r.stats.StreamReloads(), r.stats.ReadErrors(), r.stats.StreamUp(), s.closed.Load(), slotCalls, starts)
		}
		if !r.cache.Ready() || displayName(r.cache, "shop", "lan") != "Lan" || len(r.cache.Token()) != 0 {
			t.Fatalf("ready=%v lan=%q token=%q during the reload; want the old cache served without a token",
				r.cache.Ready(), displayName(r.cache, "shop", "lan"), r.cache.Token())
		}
		close(gate)
		synctest.Wait()
		if displayName(r.cache, "shop", "mai") != "Mai" || displayName(r.cache, "shop", "lan") != "" || string(r.cache.Token()) != "T1" || r.loaded.Load() != 2 {
			t.Fatalf("mai=%q lan=%q token=%q loaded=%d; want the new map from T1", displayName(r.cache, "shop", "mai"),
				displayName(r.cache, "shop", "lan"), r.cache.Token(), r.loaded.Load())
		}
		if _, releases := r.src.slot(t, 1).counts(); releases != 1 || r.src.stream(t, 1).from != "T1" || !r.stats.StreamUp() {
			t.Fatalf("reload slot releases=%d or stream not reopened from T1", releases)
		}
	})
}

func TestMirrorReloadsRevocationsEveryRecheckWithoutEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, entry("shop", "lan", "Lan", 1))
		r.recheck = 5 * time.Second
		r.start(t)
		synctest.Wait()
		if r.src.count() != 1 {
			t.Fatalf("revocation loads after the load = %d; want 1", r.src.count())
		}
		time.Sleep(11 * time.Second)
		synctest.Wait()
		if r.src.count() != 3 || r.stats.RevocationErrors() != 0 {
			t.Fatalf("revocation loads after 11s idle = %d errors=%d; want 3", r.src.count(), r.stats.RevocationErrors())
		}
		r.src.fakeRevocations.set(auth.RevocationSnapshot{}, errDown)
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if r.stats.RevocationErrors() != 1 || !r.cache.Ready() {
			t.Fatalf("revocation errors=%d ready=%v after a failed reload", r.stats.RevocationErrors(), r.cache.Ready())
		}
	})
}

func TestMirrorStopClosesTheStreamWithALiveContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		s := r.src.stream(t, 0)
		r.stop(t)
		if s.closed.Load() != 1 || s.closeErr.Load() != "<nil>" || r.stats.StreamUp() {
			t.Fatalf("closed=%d close ctx err=%v up=%v; want one close with a live context", s.closed.Load(), s.closeErr.Load(), r.stats.StreamUp())
		}
	})
}

func lostOpens(n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = fmt.Errorf("%w: history gone", store.ErrStreamLost)
	}
	return out
}

func TestMirrorBacksOffWhenTheStreamKeepsLosingItsPosition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newMirrorRig(t, entry("shop", "lan", "Lan", 1))
		r.src.set(func(f *fakeSource) { f.openErrs = lostOpens(10) })
		r.start(t)
		synctest.Wait()
		if !r.cache.Ready() || r.stats.StreamReloads() != 1 {
			t.Fatalf("ready=%v reloads=%d after the first load; want ready and 1", r.cache.Ready(), r.stats.StreamReloads())
		}
		time.Sleep(19 * time.Second)
		synctest.Wait()
		var slotCalls []time.Time
		r.src.read(func(f *fakeSource) { slotCalls = slices.Clone(f.slotCalls) })
		got := gaps(slotCalls)
		if len(got) != 4 {
			t.Fatalf("load gaps = %v; want 4 spaced loads", got)
		}
		for i, gap := range got {
			want := time.Second << i
			if gap < want*8/10 || gap > want*12/10 {
				t.Fatalf("load gaps = %v; want 1s, 2s, 4s, 8s ±20%%", got)
			}
		}
		if r.stats.StreamReloads() != 5 || r.loaded.Load() != 5 || !r.cache.Ready() || r.stats.ReadErrors() != 0 {
			t.Fatalf("reloads=%d loaded=%d ready=%v read errors=%d; want 5, 5, ready, 0",
				r.stats.StreamReloads(), r.loaded.Load(), r.cache.Ready(), r.stats.ReadErrors())
		}
		r.stop(t)
	})
}

func TestMirrorResetsTheReloadBackoffAfterAnAppliedEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		lost := streamResult{err: fmt.Errorf("%w: history gone", store.ErrStreamLost)}
		reloadWait := func(n int) time.Duration {
			t.Helper()
			lostAt := time.Now()
			r.src.stream(t, n).send(lost)
			time.Sleep(3 * time.Second)
			synctest.Wait()
			var slotCalls []time.Time
			r.src.read(func(f *fakeSource) { slotCalls = slices.Clone(f.slotCalls) })
			if len(slotCalls) != n+2 {
				t.Fatalf("loads = %d after lost position %d; want %d", len(slotCalls), n+1, n+2)
			}
			return slotCalls[n+1].Sub(lostAt)
		}
		first, second := reloadWait(0), reloadWait(1)
		if first < 800*time.Millisecond || first > 1200*time.Millisecond || second < 1600*time.Millisecond || second > 2400*time.Millisecond {
			t.Fatalf("reload waits = %v, %v; want 1s then 2s ±20%%", first, second)
		}
		r.src.stream(t, 2).send(userEvent(entry("shop", "mai", "Mai", 1), "T9"))
		synctest.Wait()
		if third := reloadWait(2); third < 800*time.Millisecond || third > 1200*time.Millisecond {
			t.Fatalf("reload wait after an applied event = %v; want 1s ±20%%", third)
		}
		if r.stats.StreamReloads() != 3 || !r.cache.Ready() {
			t.Fatalf("reloads=%d ready=%v; want 3 and ready", r.stats.StreamReloads(), r.cache.Ready())
		}
	})
}
