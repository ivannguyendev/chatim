package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestLoginSeedsLeftMarksSoAStaleMemberAddedIsDroppedEvenAfterReady(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.route(withSeq(msg("t1", 99), markSeq))
		f.loader.setLeft("t1", "u", Room{ID: orderRoom, Ver: 3})
		_, s := f.connect("t1", "u", 1)
		if got := s.take(); !slices.Equal(got, []string{"ready"}) {
			t.Fatalf("frames = %v", got)
		}
		f.route(withSeq(added("t1", orderRoom, "u", 2), markSeq+1))
		f.route(msg("t1", orderRoom))
		if got := s.take(); len(got) != 0 || f.table.stats.pairs.Load() != 1 {
			t.Fatalf("a member_added older than the left mark reached the user: %v", got)
		}
		f.route(added("t1", orderRoom, "u", 4))
		f.route(msg("t1", orderRoom))
		if got := s.take(); !slices.Equal(got, []string{"event member_added/t1/7/u/v4", "event msg_created/t1/7//v0"}) {
			t.Fatalf("frames after a real rejoin = %v", got)
		}
	})
}

func TestLeftMarksFromTheLoadDropAStaleMemberAddedHeldDuringIt(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.route(withSeq(msg("t1", 99), markSeq))
		f.loader.holding(true)
		s := &fakeSink{}
		f.table.Register("t1", "u", s)
		synctest.Wait()
		f.route(withSeq(added("t1", orderRoom, "u", 2), markSeq+1))
		f.loader.replies <- reply{left: []Room{{ID: orderRoom, Ver: 3}}}
		synctest.Wait()
		f.route(msg("t1", orderRoom))
		if got := s.take(); !slices.Equal(got, []string{"ready"}) || f.table.stats.pairs.Load() != 0 {
			t.Fatalf("frames = %v pairs %d", got, f.table.stats.pairs.Load())
		}
	})
}

func TestAJumpSuspendsEveryUserUntilTheirReloadThenResyncsAndDelivers(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		pub := NewPublisher(f.table)
		c1, s1 := f.connect("t1", "u1", 1, 2)
		c1.Open("2")
		_, s2 := f.connect("t1", "u2", 1, 2)
		s1.take()
		s2.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(40))
		synctest.Wait()
		if f.table.stats.suspended.Load() != 2 {
			t.Fatalf("suspended = %d", f.table.stats.suspended.Load())
		}
		f.route(withSeq(msg("t1", 1), 41))
		f.route(event("reaction_changed", "t1", 2, "", 0))
		f.route(event("read_updated", "t1", 1, "u1", 0))
		f.route(event("read_updated", "t1", 1, "u2", 0))
		f.route(event("member_role_changed", "t1", 2, "u2", 3))
		pub.Deliver(publishEnv("t1", packet("t1", "mention", "u1", "u2")))
		if a, b := s1.take(), s2.take(); len(a) != 0 || len(b) != 0 {
			t.Fatalf("suspended users received %v and %v", a, b)
		}
		first, firstSink, secondSink := f.loader.callOrder()[2], s1, s2
		if first == "u2" {
			firstSink, secondSink = s2, s1
		}
		f.loader.answer(2, reply{rooms: []Room{{ID: 1, Ver: 1}, {ID: 2, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 1))
		pub.Deliver(publishEnv("t1", packet("t1", "mention", "u1", "u2")))
		want := []string{"resync lag_too_old", "event msg_created/t1/1//v0", "other"}
		if got := firstSink.take(); !slices.Equal(got, want) {
			t.Fatalf("%s frames = %v, want %v", first, got, want)
		}
		if got := secondSink.take(); len(got) != 0 || f.table.stats.suspended.Load() != 1 {
			t.Fatalf("the user still waiting got %v", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		f.loader.answer(3, reply{rooms: []Room{{ID: 1, Ver: 1}, {ID: 2, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 2))
		if got := secondSink.take(); !slices.Equal(got, []string{"resync lag_too_old", "event msg_created/t1/2//v0"}) {
			t.Fatalf("second user frames after its reload = %v", got)
		}
		if got := firstSink.take(); !slices.Equal(got, []string{"event msg_created/t1/2//v0"}) {
			t.Fatalf("first user frames = %v", got)
		}
		f.route(event("reaction_changed", "t1", 2, "", 0))
		if got, other := s1.take(), s2.take(); !slices.Equal(got, []string{"event reaction_changed/t1/2//v0"}) || len(other) != 0 {
			t.Fatalf("viewer frames %v, other %v", got, other)
		}
		if f.table.stats.suspended.Load() != 0 || f.table.stats.reloads.Load() != 2 {
			t.Fatalf("suspended %d reloads %d", f.table.stats.suspended.Load(), f.table.stats.reloads.Load())
		}
	})
}

func TestARemovalInTheSkippedSpanEndsTheRoomForThatUserFromTheJump(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1, 2)
		c.Open("2")
		s.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(10))
		synctest.Wait()
		f.route(withSeq(msg("t1", 2), 11))
		f.route(withSeq(event("reaction_changed", "t1", 2, "", 0), 12))
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}, left: []Room{{ID: 2, Ver: 2}}})
		synctest.Wait()
		if got := s.take(); !slices.Equal(got, []string{"closed 2 removed", "resync lag_too_old"}) {
			t.Fatalf("frames = %v", got)
		}
		f.route(withSeq(added("t1", 2, "u", 2), 20))
		f.route(msg("t1", 2))
		f.route(event("reaction_changed", "t1", 2, "", 0))
		if got := s.take(); len(got) != 0 || f.table.stats.pairs.Load() != 1 {
			t.Fatalf("the removed user still hears room 2: %v", got)
		}
		f.route(added("t1", 2, "u", 3))
		if got := s.take(); !slices.Equal(got, []string{"event member_added/t1/2/u/v3"}) {
			t.Fatalf("a newer member_added = %v", got)
		}
	})
}

func TestMemberChangesDuringAReloadWinOverTheLoadedListWithoutReachingTheUser(t *testing.T) {
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
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		f.route(withSeq(msg("t1", 1), 53))
		f.route(withSeq(msg("t1", 4), 54))
		want := []string{"event msg_created/t1/1//v0", "resync lag_too_old", "event msg_created/t1/4//v0"}
		if got := s.take(); !slices.Equal(got, want) {
			t.Fatalf("frames = %v, want %v", got, want)
		}
	})
}

func TestASecondDeviceOfASuspendedUserWaitsAndGetsReadyAfterTheReload(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, first := f.connect("t1", "u", 1)
		first.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		second := &fakeSink{}
		c2 := f.table.Register("t1", "u", second)
		c2.Open("1")
		f.route(withSeq(event("reaction_changed", "t1", 1, "", 0), 6))
		if got := second.take(); len(got) != 0 {
			t.Fatalf("the second device got %v before the reload", got)
		}
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		c2.Open("1")
		f.route(event("reaction_changed", "t1", 1, "", 0))
		if got := first.take(); !slices.Equal(got, []string{"resync lag_too_old"}) {
			t.Fatalf("first device frames = %v", got)
		}
		if got := second.take(); !slices.Equal(got, []string{"ready", "event reaction_changed/t1/1//v0"}) {
			t.Fatalf("second device frames = %v", got)
		}
	})
}

func TestAnOpenRoomWaitingForMemberAddedAtTheJumpIsDropped(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1)
		c.Open("9")
		s.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := s.take(); len(got) != 0 || len(c.pending) != 0 {
			t.Fatalf("frames %v pending %d", got, len(c.pending))
		}
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}, {ID: 9, Ver: 1}}})
		synctest.Wait()
		c.Open("9")
		f.route(event("reaction_changed", "t1", 9, "", 0))
		if got := s.take(); !slices.Equal(got, []string{"resync lag_too_old", "event reaction_changed/t1/9//v0"}) {
			t.Fatalf("frames = %v", got)
		}
	})
}

func TestAStaleLoadFromBeforeTheJumpIsDiscarded(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		f.table.Register("t1", "u", s)
		synctest.Wait()
		f.table.Jumped(lagJump(8))
		synctest.Wait()
		f.loader.answer(0, reply{rooms: []Room{{ID: 9, Ver: 1}}})
		synctest.Wait()
		if got := s.take(); len(got) != 0 || f.table.stats.suspended.Load() != 1 {
			t.Fatalf("a load started before the jump was used: %v", got)
		}
		f.loader.answer(1, reply{rooms: []Room{{ID: 2, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 9))
		f.route(msg("t1", 2))
		if got := s.take(); !slices.Equal(got, []string{"ready", "event msg_created/t1/2//v0"}) || f.table.stats.suspended.Load() != 0 {
			t.Fatalf("frames = %v", got)
		}
	})
}

func TestAStaleFailedLoadFromBeforeTheJumpClosesNothing(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{}
		f.table.Register("t1", "u", s)
		synctest.Wait()
		f.table.Jumped(lagJump(8))
		synctest.Wait()
		f.loader.answer(0, reply{err: errors.New("core down")})
		synctest.Wait()
		if s.failures() != 0 || len(s.take()) != 0 || f.table.stats.loadFailures.Load() != 0 || f.table.stats.reloadFailures.Load() != 0 {
			t.Fatalf("failures %d load failures %d", s.failures(), f.table.stats.loadFailures.Load())
		}
	})
}

func TestASuspendedSlowDeviceGetsNoClientBacklogResync(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		slow, s := f.connect("t1", "slow", 1)
		slow.Open("1")
		s.take()
		s.refusing(refuseAll)
		f.route(catchingUp(msg("t1", 1)))
		f.loader.holding(true)
		f.table.Jumped(lagJump(f.seq))
		synctest.Wait()
		s.refusing(acceptAll)
		f.route(msg("t1", 1))
		if got := s.take(); len(got) != 0 || f.table.backlogged.Load() != 0 {
			t.Fatalf("a suspended device got %v, backlogged %d", got, f.table.backlogged.Load())
		}
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 1))
		if got := s.take(); !slices.Equal(got, []string{"resync lag_too_old", "event msg_created/t1/1//v0"}) || backlogResyncs(f) != 0 {
			t.Fatalf("frames = %v backlog resyncs %d", got, backlogResyncs(f))
		}
	})
}
