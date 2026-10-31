package directory_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func (r *rig) changesCalls() int {
	var n int
	r.reader.read(func(f *fakeReader) { n = f.changesCalls })
	return n
}

func TestFollowerAppliesOnNotifyAndOnTheTwoSecondRecheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		l := newFakeListener()
		f := r.startFollower(&listenFactory{listeners: []*fakeListener{l}})
		defer f.stop(t)
		began := time.Now()
		synctest.Wait()
		r.reader.add(userChange("shop", "lan", "Lan", 1))
		l.events <- listenEvent{notice: store.NoticeDirectory}
		synctest.Wait()
		if got := displayName(r.cache, "shop", "lan"); got != "Lan" || time.Since(began) != 0 {
			t.Fatalf("after notify name = %q at %v; want Lan at once", got, time.Since(began))
		}
		if !r.stats.LastCaughtUp().Equal(began) || r.cache.Position() != (store.Position{Xid: 1}) {
			t.Fatalf("caught up at %v position %+v; want %v at xid 1", r.stats.LastCaughtUp(), r.cache.Position(), began)
		}
		r.reader.add(userChange("shop", "minh", "Minh", 1))
		time.Sleep(2*time.Second - time.Millisecond)
		synctest.Wait()
		if got := displayName(r.cache, "shop", "minh"); got != "" {
			t.Fatalf("before the recheck name = %q; want missing", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if got := displayName(r.cache, "shop", "minh"); got != "Minh" {
			t.Fatalf("after the recheck name = %q; want Minh", got)
		}
		time.Sleep(time.Second)
		l.events <- listenEvent{notice: store.NoticeOther}
		synctest.Wait()
		r.reader.add(userChange("shop", "an", "An", 1))
		time.Sleep(time.Second)
		synctest.Wait()
		if got := displayName(r.cache, "shop", "an"); got != "An" {
			t.Fatalf("a notice must not delay the recheck: name = %q; want An", got)
		}
	})
}

func TestFollowerReconnectsTheListener(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		first, second := newFakeListener(), newFakeListener()
		factory := &listenFactory{failFirst: 1, listeners: []*fakeListener{first, second}}
		f := r.startFollower(factory)
		defer f.stop(t)
		synctest.Wait()
		if r.stats.ListenUp() {
			t.Fatal("listen up after a failed open")
		}
		r.reader.add(userChange("shop", "lan", "Lan", 1))
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := displayName(r.cache, "shop", "lan"); got != "Lan" {
			t.Fatalf("recheck without a listener: name = %q; want Lan", got)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if !r.stats.ListenUp() || len(factory.callTimes()) != 2 {
			t.Fatalf("listen up=%v opens=%d; want reconnected on the second try", r.stats.ListenUp(), len(factory.callTimes()))
		}
		revsBefore := r.revs.count()
		r.reader.add(userChange("shop", "minh", "Minh", 1))
		first.events <- listenEvent{err: store.ErrListenerBroken}
		synctest.Wait()
		if !first.isClosed() || r.stats.ListenUp() {
			t.Fatalf("broken listener closed=%v listen up=%v; want closed and down", first.isClosed(), r.stats.ListenUp())
		}
		if displayName(r.cache, "shop", "minh") != "Minh" || r.revs.count() != revsBefore+1 {
			t.Fatal("a broken listener must run both passes at once")
		}
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if !r.stats.ListenUp() || len(factory.callTimes()) != 3 {
			t.Fatalf("listen up=%v opens=%d; want reconnected to the second listener", r.stats.ListenUp(), len(factory.callTimes()))
		}
		r.reader.add(userChange("shop", "an", "An", 1))
		second.events <- listenEvent{notice: store.NoticeDirectory}
		synctest.Wait()
		if displayName(r.cache, "shop", "an") != "An" {
			t.Fatal("the new listener does not drive passes")
		}
		f.stop(t)
		if !second.isClosed() || r.stats.ListenUp() {
			t.Fatalf("on exit closed=%v listen up=%v; want the listener closed", second.isClosed(), r.stats.ListenUp())
		}
	})
}

func TestDirectoryNotifyDoesNotFetchRevocations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		l := newFakeListener()
		f := r.startFollower(&listenFactory{listeners: []*fakeListener{l}})
		defer f.stop(t)
		synctest.Wait()
		for range 3 {
			l.events <- listenEvent{notice: store.NoticeDirectory}
			synctest.Wait()
		}
		if r.revs.count() != 0 || r.changesCalls() != 3 {
			t.Fatalf("revocation fetches=%d feed reads=%d; want 0 and 3", r.revs.count(), r.changesCalls())
		}
	})
}

func TestRevocationsAreMergedAndPrunedOnRevOrTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		l := newFakeListener()
		f := r.startFollower(&listenFactory{listeners: []*fakeListener{l}})
		defer f.stop(t)
		synctest.Wait()
		claims := func(session string) jwtverify.Claims {
			return jwtverify.Claims{Tenant: "shop", Session: session, IssuedAt: time.Now()}
		}
		r.revs.set(revocation.Snapshot{Sessions: []revocation.Session{{ID: "s1", Tenant: "shop", ExpiresAt: time.Now().Add(3 * time.Second)}}}, nil)
		l.events <- listenEvent{notice: store.NoticeRevocations}
		synctest.Wait()
		if r.list.Check(claims("s1")) != revocation.SessionRevoked || r.changesCalls() != 0 {
			t.Fatalf("after rev: s1 %v feed reads %d; want revoked and no feed read", r.list.Check(claims("s1")), r.changesCalls())
		}
		r.revs.set(revocation.Snapshot{Sessions: []revocation.Session{{ID: "s2", Tenant: "shop", ExpiresAt: time.Now().Add(time.Hour)}}}, nil)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if r.list.Check(claims("s2")) != revocation.SessionRevoked {
			t.Fatal("the tick did not merge s2")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if sessions, _ := r.list.Counts(); sessions != 1 || r.list.Check(claims("s1")) != revocation.Allowed {
			t.Fatalf("sessions=%d s1=%v; want s1 pruned after it expired", sessions, r.list.Check(claims("s1")))
		}
		r.revs.set(revocation.Snapshot{}, errDown)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if r.stats.RevocationErrors() != 1 {
			t.Fatalf("revocation errors = %d; want 1", r.stats.RevocationErrors())
		}
	})
}

func TestSignalWakesEveryWaiterOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		l := newFakeListener()
		f := r.startFollower(&listenFactory{listeners: []*fakeListener{l}})
		defer f.stop(t)
		synctest.Wait()
		var mu sync.Mutex
		wakes := make([]int, 3)
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for i := range wakes {
			wg.Go(func() {
				for {
					select {
					case <-f.Signal():
						mu.Lock()
						wakes[i]++
						mu.Unlock()
					case <-stop:
						return
					}
				}
			})
		}
		synctest.Wait()
		l.events <- listenEvent{notice: store.NoticeDirectory}
		synctest.Wait()
		r.reader.add(userChange("shop", "lan", "Lan", 1))
		l.events <- listenEvent{notice: store.NoticeDirectory}
		synctest.Wait()
		r.reader.add(userChange("shop", "lan", "Lan 2", 2), groupChange("shop", "sales", "Sales", 1))
		l.events <- listenEvent{notice: store.NoticeDirectory}
		synctest.Wait()
		close(stop)
		wg.Wait()
		for i, n := range wakes {
			if n != 2 {
				t.Errorf("waiter %d woke %d times; want 2", i, n)
			}
		}
	})
}

func TestHorizonStallIsMeasuredAndCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		f := r.startFollower(&listenFactory{listeners: []*fakeListener{newFakeListener()}})
		defer f.stop(t)
		r.reader.set(func(f *fakeReader) { f.pending = true })
		time.Sleep(2 * time.Second)
		synctest.Wait()
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if got := r.stats.HorizonStalledSeconds(time.Now()); got != 4 {
			t.Fatalf("stalled = %vs; want 4s, measured from the first stalled pass", got)
		}
		r.reader.set(func(f *fakeReader) { f.pending = false })
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := r.stats.HorizonStalledSeconds(time.Now()); got != 0 {
			t.Fatalf("stalled = %vs after the horizon moved; want 0", got)
		}
		r.reader.set(func(f *fakeReader) { f.changesErr = errDown })
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if r.stats.ReadErrors() != 1 {
			t.Fatalf("read errors = %d; want 1", r.stats.ReadErrors())
		}
	})
}
