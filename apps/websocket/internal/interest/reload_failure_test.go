package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestAUserWhoseReloadKeepsFailingIsRetriedAboutOncePerSecondWhileOthersLoad(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		sinks := map[string]*fakeSink{}
		for _, u := range []string{"bad", "a", "b", "c"} {
			_, sinks[u] = f.connect("t1", u, 1)
			sinks[u].take()
		}
		f.loader.failing("t1", "bad", errors.New("poison"))
		f.table.Jumped(lagJump(5))
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		for _, u := range []string{"a", "b", "c"} {
			if got := sinks[u].take(); !slices.Equal(got, []string{"resync lag_too_old"}) {
				t.Fatalf("%s frames = %v", u, got)
			}
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if calls := f.loader.callsOf("t1", "bad") - 1; calls < 10 || calls > 11 {
			t.Fatalf("reloads of the failing user in 10s = %d, want about one per second", calls)
		}
		if sinks["bad"].failures() != 0 || len(sinks["bad"].take()) != 0 || f.table.stats.suspended.Load() != 1 || f.table.stats.reloads.Load() != 3 {
			t.Fatalf("bad failures %d suspended %d reloads %d", sinks["bad"].failures(), f.table.stats.suspended.Load(), f.table.stats.reloads.Load())
		}
	})
}

func TestAFailedReloadOfAUserNeverReadyClosesItsConnectionsLikeALogin(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		c := f.table.Register("t1", "u", s)
		synctest.Wait()
		f.table.Jumped(lagJump(8))
		synctest.Wait()
		f.loader.answer(1, reply{err: errors.New("core down")})
		synctest.Wait()
		if s.failures() != 1 || len(s.take()) != 0 || f.table.stats.users.Load() != 0 || f.table.stats.suspended.Load() != 0 {
			t.Fatalf("failures %d users %d suspended %d", s.failures(), f.table.stats.users.Load(), f.table.stats.suspended.Load())
		}
		if f.table.stats.loadFailures.Load() != 1 || f.table.stats.reloadFailures.Load() != 0 {
			t.Fatalf("load failures %d reload failures %d", f.table.stats.loadFailures.Load(), f.table.stats.reloadFailures.Load())
		}
		c.Unregister()
	})
}

func TestAFailedReloadOfAUserWithAWaitingDeviceGoesToTheFront(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		for _, u := range []string{"x", "y", "z"} {
			f.connect("t1", u, 1)
		}
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		first := f.loader.callOrder()[3]
		f.table.resyncMu.Lock()
		mixed := f.table.reloads.queue[len(f.table.reloads.queue)-1].key.user
		f.table.resyncMu.Unlock()
		f.table.Register("t1", mixed, &fakeSink{})
		time.Sleep(time.Second)
		synctest.Wait()
		f.loader.answer(4, reply{err: errors.New("core down")})
		time.Sleep(time.Second)
		synctest.Wait()
		if got := f.loader.callOrder()[3:]; len(got) != 3 || got[0] != first || got[1] != mixed || got[2] != mixed {
			t.Fatalf("reloads = %v, want %s, then %s twice", got, first, mixed)
		}
	})
}

func TestAJumpAfterCloseSuspendsNobody(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.connect("t1", "u", 1)
		f.table.Close()
		f.table.Jumped(lagJump(3))
		synctest.Wait()
		if f.table.stats.suspended.Load() != 0 || f.loader.callsOf("t1", "u") != 1 {
			t.Fatalf("suspended %d loads %d", f.table.stats.suspended.Load(), f.loader.callsOf("t1", "u"))
		}
	})
}
