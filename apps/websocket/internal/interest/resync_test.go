package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
)

func lagJump(seq uint64) feed.Jump { return feed.Jump{Reason: feed.JumpLagTooOld, Seq: seq} }

func TestASkipReloadsEveryUserSpreadOverTheWindowAndSendsResync(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c1, s1 := f.connect("t1", "u1", 1, 2)
		_, s2 := f.connect("t1", "u2", 1)
		c1.Open("2")
		s1.take()
		s2.take()
		f.loader.set("t1", "u1", 1)
		f.loader.set("t1", "u2", 1, 3)
		start := time.Now()
		f.table.Jumped(lagJump(40))
		synctest.Wait()
		first, second := s1, s2
		if s2.pending() > 0 {
			first, second = s2, s1
		}
		if got := first.take(); len(got) == 0 || got[len(got)-1] != "resync lag_too_old" {
			t.Fatalf("first user frames at the start = %v", got)
		}
		if got := second.take(); len(got) != 0 {
			t.Fatalf("second user reloaded at once: %v", got)
		}
		time.Sleep(30*time.Second - time.Nanosecond)
		synctest.Wait()
		if got := second.take(); len(got) != 0 {
			t.Fatalf("second user reloaded before its turn: %v", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := second.take(); len(got) == 0 || got[len(got)-1] != "resync lag_too_old" || time.Since(start) != 30*time.Second {
			t.Fatalf("second user frames at 30s = %v", got)
		}
		f.route(msg("t1", 2))
		f.route(msg("t1", 3))
		if got, want := s1.take(), []string(nil); !slices.Equal(got, want) {
			t.Fatalf("u1 still receives room 2 or room 3: %v", got)
		}
		if got := s2.take(); !slices.Equal(got, []string{"event msg_created/t1/3//v0"}) {
			t.Fatalf("u2 frames for room 3 = %v", got)
		}
		if f.table.stats.reloads.Load() != 2 || f.table.stats.resyncs[1].Load() != 2 || f.loader.callsOf("t1", "u1") != 2 {
			t.Fatalf("reloads %d resyncs %d", f.table.stats.reloads.Load(), f.table.stats.resyncs[1].Load())
		}
	})
}

func TestAReloadClosesOpenRoomsTheUserLeftDuringTheSkip(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1, 2)
		c.Open("2")
		c.Open("7")
		s.take()
		f.loader.set("t1", "u", 1, 7)
		f.table.Jumped(lagJump(10))
		synctest.Wait()
		if got := s.take(); !slices.Equal(got, []string{"closed 2 removed", "resync lag_too_old"}) {
			t.Fatalf("frames = %v", got)
		}
		f.route(event("reaction_changed", "t1", 7, "", 0))
		f.route(event("reaction_changed", "t1", 2, "", 0))
		if got := s.take(); !slices.Equal(got, []string{"event reaction_changed/t1/7//v0"}) {
			t.Fatalf("after reload the open rooms got %v", got)
		}
		f.route(withSeq(added("t1", 2, "u", 1), 20))
		if got := s.take(); len(got) != 0 {
			t.Fatalf("a replayed member_added at the old ver brought room 2 back: %v", got)
		}
		f.route(withSeq(added("t1", 2, "u", 2), 21))
		if got := s.take(); !slices.Equal(got, []string{"event member_added/t1/2/u/v2"}) {
			t.Fatalf("a newer member_added = %v", got)
		}
	})
}

func TestMemberChangesDuringAReloadWinOverTheOlderList(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		f.loader.holding(true)
		f.route(withSeq(msg("t1", 1), 50))
		f.table.Jumped(lagJump(50))
		synctest.Wait()
		f.route(withSeq(removed("t1", 1, "u", 2), 51))
		f.route(withSeq(added("t1", 4, "u", 1), 52))
		f.loader.replies <- reply{rooms: []Room{{ID: 1, Ver: 1}}}
		synctest.Wait()
		f.route(withSeq(msg("t1", 1), 53))
		f.route(withSeq(msg("t1", 4), 54))
		want := []string{"event msg_created/t1/1//v0", "event member_removed/t1/1/u/v2", "event member_added/t1/4/u/v1", "resync lag_too_old", "event msg_created/t1/4//v0"}
		if got := s.take(); !slices.Equal(got, want) {
			t.Fatalf("frames = %v, want %v", got, want)
		}
	})
}

func TestARecreatedStreamResetsTheMarksSoItsEventsApply(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.route(withSeq(msg("t1", 1), 500))
		_, s1 := f.connect("t1", "u1", 1)
		_, s2 := f.connect("t1", "u2", 1)
		s1.take()
		s2.take()
		f.table.Jumped(feed.Jump{Reason: feed.JumpConsumerLost, Seq: 3})
		synctest.Wait()
		late := s2
		if s1.pending() == 0 {
			late = s1
		}
		late.take()
		lateUser := "u2"
		if late == s1 {
			lateUser = "u1"
		}
		f.route(withSeq(added("t1", 9, lateUser, 1), 4))
		f.route(withSeq(msg("t1", 9), 5))
		if got := late.take(); !slices.Equal(got, []string{"event member_added/t1/9/" + lateUser + "/v1", "event msg_created/t1/9//v0"}) {
			t.Fatalf("new stream events before the reload = %v", got)
		}
		f.loader.set("t1", lateUser, 1, 9)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if got := late.take(); !slices.Equal(got, []string{"resync consumer_lost"}) {
			t.Fatalf("late user frames = %v", got)
		}
		f.route(withSeq(removed("t1", 9, lateUser, 2), 6))
		if got := late.take(); !slices.Equal(got, []string{"event member_removed/t1/9/" + lateUser + "/v2"}) {
			t.Fatalf("after the reload new stream events = %v", got)
		}
		if f.table.stats.resyncs[2].Load() != 2 {
			t.Fatalf("consumer_lost resyncs = %d", f.table.stats.resyncs[2].Load())
		}
	})
}

func TestAUserStillLoadingAtTheSkipLoadsAgainAndOnlyGetsReady(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		f.table.Register("t1", "u", s)
		synctest.Wait()
		f.table.Jumped(lagJump(8))
		synctest.Wait()
		f.loader.replies <- reply{rooms: []Room{{ID: 2, Ver: 1}}}
		f.loader.replies <- reply{rooms: []Room{{ID: 2, Ver: 1}}}
		synctest.Wait()
		f.route(msg("t1", 2))
		if got := s.take(); !slices.Equal(got, []string{"ready", "event msg_created/t1/2//v0"}) || f.loader.callsOf("t1", "u") != 2 {
			t.Fatalf("frames = %v, loads %d", got, f.loader.callsOf("t1", "u"))
		}
	})
}

func TestAFailedReloadClosesTheConnectionsOfTheUser(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1, 2)
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		f.loader.replies <- reply{err: errors.New("core down")}
		synctest.Wait()
		if s.failures() != 1 || f.table.stats.users.Load() != 0 || f.table.stats.pairs.Load() != 0 {
			t.Fatalf("failures %d users %d pairs %d", s.failures(), f.table.stats.users.Load(), f.table.stats.pairs.Load())
		}
		c.Unregister()
	})
}

func TestASecondSkipRestartsTheSchedule(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s1 := f.connect("t1", "u1", 1)
		_, s2 := f.connect("t1", "u2", 1)
		s1.take()
		s2.take()
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		time.Sleep(10 * time.Second)
		f.table.Jumped(feed.Jump{Reason: feed.JumpConsumerLost, Seq: 9})
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		calls := f.loader.callsOf("t1", "u1") + f.loader.callsOf("t1", "u2")
		if calls != 5 || f.table.stats.resyncs[1].Load() != 1 || f.table.stats.resyncs[2].Load() != 2 {
			t.Fatalf("loads %d lag resyncs %d lost resyncs %d", calls, f.table.stats.resyncs[1].Load(), f.table.stats.resyncs[2].Load())
		}
	})
}
