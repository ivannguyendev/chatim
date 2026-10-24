package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestOpenRoomWhileSuspendedOrAfterAFailedLoadIsIgnored(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1)
		s.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		c.Open("1")
		f.loader.answer(1, reply{err: errors.New("core down")})
		synctest.Wait()
		c.Open("1")
		if got := s.take(); !slices.Equal(got, []string{"load_failed"}) || len(c.open) != 0 || len(c.pending) != 0 {
			t.Fatalf("frames %v open %d pending %d", got, len(c.open), len(c.pending))
		}
		c.LoadRooms()
		time.Sleep(time.Millisecond)
		synctest.Wait()
		f.loader.answer(2, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		c.Open("1")
		f.route(event("reaction_changed", "t1", 1, "", 0))
		if got := s.take(); !slices.Equal(got, []string{"resync lag_too_old", "event reaction_changed/t1/1//v0"}) {
			t.Fatalf("frames after the reload = %v", got)
		}
	})
}

func TestMemberChangesWhileALoadFailedUserWaitsAreDroppedAndTheNextLoadDecides(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		c := mustRegister(t, f.table, "t1", "u", s)
		synctest.Wait()
		f.loader.answer(0, reply{err: errors.New("core down")})
		synctest.Wait()
		s.take()
		f.route(withSeq(added("t1", 1, "u", 1), 9))
		f.route(withSeq(added("t1", 5, "u", 1), 10))
		if u := f.table.lookup(userKey{tenant: "t1", user: "u"}); len(u.held) != 0 {
			t.Fatalf("held %d member changes while failed and not loading", len(u.held))
		}
		c.LoadRooms()
		synctest.Wait()
		f.route(withSeq(added("t1", 7, "u", 1), 11))
		f.loader.answer(1, reply{rooms: []Room{{ID: 5, Ver: 1}}, left: []Room{{ID: 1, Ver: 2}}})
		synctest.Wait()
		f.route(withSeq(added("t1", 1, "u", 1), 9))
		for _, room := range []uint64{1, 5, 7} {
			f.route(msg("t1", room))
		}
		want := []string{"ready", "event msg_created/t1/5//v0", "event msg_created/t1/7//v0"}
		if got := s.take(); !slices.Equal(got, want) || f.table.stats.pairs.Load() != 2 {
			t.Fatalf("frames %v pairs %d, want rooms 5 and 7 only", got, f.table.stats.pairs.Load())
		}
	})
}
