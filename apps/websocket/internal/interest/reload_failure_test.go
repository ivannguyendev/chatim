package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestAFailedReloadSendsLoadFailedAndWaitsForTheClientToAskAgain(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		conns, sinks := map[string]*Conn{}, map[string]*fakeSink{}
		for _, u := range []string{"u1", "u2", "u3"} {
			conns[u], sinks[u] = f.connect("t1", u, 1)
			sinks[u].take()
		}
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		f.table.resyncMu.Lock()
		failing := f.table.reloads.queue[len(f.table.reloads.queue)-1].key.user
		f.table.resyncMu.Unlock()
		waiting := &fakeSink{}
		second := mustRegister(t, f.table, "t1", failing, waiting)
		f.loader.answer(3, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		time.Sleep(time.Second)
		synctest.Wait()
		f.loader.answer(4, reply{err: errors.New("core down")})
		synctest.Wait()
		for i, s := range []*fakeSink{sinks[failing], waiting} {
			if got := s.take(); !slices.Equal(got, []string{"load_failed"}) || s.failures() != 0 {
				t.Fatalf("device %d frames %v failures %d", i, got, s.failures())
			}
		}
		if f.table.stats.users.Load() != 3 || f.table.stats.suspended.Load() != 2 || f.table.stats.reloadFailures.Load() != 1 || f.table.stats.loadFailures.Load() != 0 {
			t.Fatalf("users %d suspended %d reload failures %d", f.table.stats.users.Load(), f.table.stats.suspended.Load(), f.table.stats.reloadFailures.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		f.loader.answer(5, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		time.Sleep(time.Hour)
		synctest.Wait()
		f.route(msg("t1", 1))
		if got := len(f.loader.callOrder()); got != 6 || len(sinks[failing].take()) != 0 || len(waiting.take()) != 0 || f.table.stats.suspended.Load() != 1 {
			t.Fatalf("loads %d, suspended %d; the server reloaded or delivered without load_rooms", got, f.table.stats.suspended.Load())
		}
		conns["u1"].LoadRooms()
		if failing == "u1" {
			conns["u2"].LoadRooms()
		}
		second.LoadRooms()
		conns[failing].LoadRooms()
		synctest.Wait()
		if got := f.loader.callOrder(); len(got) != 7 || got[6] != failing {
			t.Fatalf("loads after load_rooms = %v, want one more for %s", got, failing)
		}
		f.loader.answer(6, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 1))
		if got := sinks[failing].take(); !slices.Equal(got, []string{"resync lag_too_old", "event msg_created/t1/1//v0"}) {
			t.Fatalf("readied device frames = %v", got)
		}
		if got := waiting.take(); !slices.Equal(got, []string{"ready", "event msg_created/t1/1//v0"}) || f.table.stats.suspended.Load() != 0 {
			t.Fatalf("waiting device frames = %v", got)
		}
	})
}

func TestAFailedFirstLoadSendsLoadFailedAndLoadRoomsReadiesTheUser(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s1, s2 := &fakeSink{}, &fakeSink{}
		c1 := mustRegister(t, f.table, "t1", "u", s1)
		c2 := mustRegister(t, f.table, "t1", "u", s2)
		synctest.Wait()
		f.loader.answer(0, reply{err: errors.New("core down")})
		synctest.Wait()
		for i, s := range []*fakeSink{s1, s2} {
			if got := s.take(); !slices.Equal(got, []string{"load_failed"}) || s.failures() != 0 {
				t.Fatalf("device %d frames %v failures %d", i, got, s.failures())
			}
		}
		f.route(added("t1", 1, "u", 1))
		late := &fakeSink{}
		mustRegister(t, f.table, "t1", "u", late)
		time.Sleep(time.Hour)
		synctest.Wait()
		u := f.table.lookup(userKey{tenant: "t1", user: "u"})
		if got := late.take(); !slices.Equal(got, []string{"load_failed"}) || len(u.held) != 0 || f.loader.callsOf("t1", "u") != 1 {
			t.Fatalf("late device %v, held %d, loads %d", got, len(u.held), f.loader.callsOf("t1", "u"))
		}
		if f.table.stats.loadFailures.Load() != 1 || f.table.stats.reloadFailures.Load() != 0 || f.table.stats.suspended.Load() != 0 || f.table.stats.users.Load() != 1 {
			t.Fatalf("load failures %d suspended %d users %d", f.table.stats.loadFailures.Load(), f.table.stats.suspended.Load(), f.table.stats.users.Load())
		}
		c1.LoadRooms()
		c2.LoadRooms()
		synctest.Wait()
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 1))
		for i, s := range []*fakeSink{s1, s2, late} {
			if got := s.take(); !slices.Equal(got, []string{"ready", "event msg_created/t1/1//v0"}) {
				t.Fatalf("device %d frames = %v", i, got)
			}
		}
		if f.loader.callsOf("t1", "u") != 2 {
			t.Fatalf("loads = %d, want 2", f.loader.callsOf("t1", "u"))
		}
	})
}

func TestAFailedReloadOfAUserNeverReadyKeepsItSuspendedUntilItLeaves(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		c := mustRegister(t, f.table, "t1", "u", s)
		synctest.Wait()
		f.table.Jumped(lagJump(8))
		synctest.Wait()
		f.loader.answer(1, reply{err: errors.New("core down")})
		synctest.Wait()
		if got := s.take(); !slices.Equal(got, []string{"load_failed"}) || s.failures() != 0 || f.table.stats.suspended.Load() != 1 || f.table.stats.reloadFailures.Load() != 1 {
			t.Fatalf("frames %v failures %d suspended %d", got, s.failures(), f.table.stats.suspended.Load())
		}
		c.Unregister()
		if f.table.stats.suspended.Load() != 0 || f.table.stats.users.Load() != 0 {
			t.Fatalf("suspended %d users %d after the user left", f.table.stats.suspended.Load(), f.table.stats.users.Load())
		}
	})
}

func TestLoadFailedCarriesARetryHintOfOneAndAHalfToThreeSeconds(t *testing.T) {
	for range 50 {
		var f chatimv1.ServerFrame
		if err := proto.Unmarshal(loadFailedFrame(), &f); err != nil {
			t.Fatal(err)
		}
		if ms := f.GetLoadFailed().GetRetryAfterMs(); ms < 1500 || ms > 3000 {
			t.Fatalf("retry_after_ms = %d", ms)
		}
	}
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
