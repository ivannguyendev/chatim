package interest

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
)

func TestOpenRoomsBeforeTheLoadAreASetCappedPerConnection(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		c := f.table.Register("t1", "u", s)
		synctest.Wait()
		for range 3 {
			c.Open("1")
		}
		for room := 2; room <= earlyMax; room++ {
			c.Open(strconv.Itoa(room))
		}
		c.Open("1")
		if len(c.early) != earlyMax || len(s.take()) != 0 {
			t.Fatalf("early rooms = %d, want %d with duplicates counted once", len(c.early), earlyMax)
		}
		over := strconv.Itoa(earlyMax + 1)
		c.Open(over)
		if got := s.take(); !slices.Equal(got, []string{"closed " + over + " not_member"}) || len(c.early) != earlyMax {
			t.Fatalf("frames over the cap = %v, early %d", got, len(c.early))
		}
		f.loader.replies <- reply{rooms: []Room{{ID: 1, Ver: 1}, {ID: 2, Ver: 1}}}
		synctest.Wait()
		f.route(event("reaction_changed", "t1", 1, "", 0))
		f.route(event("reaction_changed", "t1", 2, "", 0))
		want := []string{"ready", "event reaction_changed/t1/1//v0", "event reaction_changed/t1/2//v0"}
		if got := s.take(); !slices.Equal(got, want) || len(c.open) != 2 || len(c.pending) != earlyMax-2 || len(c.early) != 0 {
			t.Fatalf("frames %v open %d pending %d early %d", got, len(c.open), len(c.pending), len(c.early))
		}
		c.Unregister()
	})
}

func TestMemberChangesWhileALoadFailedUserWaitsAreDroppedAndTheNextLoadDecides(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		c := f.table.Register("t1", "u", s)
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
