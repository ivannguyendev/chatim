package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestAReadyThatDoesNotFitIsSentAgainOnceTheQueueDrains(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.set("t1", "u", 1)
		s := &fakeSink{mode: refuseAll}
		mustRegister(t, f.table, "t1", "u", s)
		synctest.Wait()
		if got := s.take(); len(got) != 0 || f.table.backlogged.Load() != 1 {
			t.Fatalf("frames %v backlogged %d", got, f.table.backlogged.Load())
		}
		s.refusing(halfFull)
		f.route(msg("t1", 2))
		if got := s.take(); len(got) != 0 {
			t.Fatalf("frames before the queue drained = %v", got)
		}
		s.refusing(acceptAll)
		f.route(msg("t1", 2))
		f.route(msg("t1", 1))
		if got := s.take(); !slices.Equal(got, []string{"ready", "event msg_created/t1/1//v0"}) || backlogResyncs(f) != 0 || f.table.backlogged.Load() != 0 {
			t.Fatalf("frames %v backlog resyncs %d backlogged %d", got, backlogResyncs(f), f.table.backlogged.Load())
		}
	})
}

func TestARoomClosedThatDoesNotFitIsFollowedByABacklogResync(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1)
		s.take()
		c.Open("9")
		s.refusing(refuseAll)
		time.Sleep(f.table.cfg.OpenPending + time.Second)
		synctest.Wait()
		if f.table.backlogged.Load() != 1 || f.table.stats.openRejected.Load() != 1 {
			t.Fatalf("backlogged %d rejected %d", f.table.backlogged.Load(), f.table.stats.openRejected.Load())
		}
		s.refusing(acceptAll)
		f.route(msg("t1", 2))
		if got := s.take(); !slices.Equal(got, []string{"resync client_backlog"}) {
			t.Fatalf("frames = %v", got)
		}
	})
}

func TestALagResyncThatDoesNotFitLeavesTheDeviceOwedABacklogResync(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(f.seq))
		synctest.Wait()
		s.refusing(refuseAll)
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		if got := s.take(); len(got) != 0 || f.table.backlogged.Load() != 1 {
			t.Fatalf("frames %v backlogged %d", got, f.table.backlogged.Load())
		}
		s.refusing(acceptAll)
		f.route(msg("t1", 1))
		if got := s.take(); !slices.Equal(got, []string{"resync client_backlog", "event msg_created/t1/1//v0"}) || backlogResyncs(f) != 1 {
			t.Fatalf("frames %v backlog resyncs %d", got, backlogResyncs(f))
		}
	})
}

func TestALoadFailedThatDoesNotFitIsSentAgainOnceTheQueueDrains(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		s := &fakeSink{mode: refuseAll}
		c := mustRegister(t, f.table, "t1", "u", s)
		synctest.Wait()
		f.loader.answer(0, reply{err: errors.New("core down")})
		synctest.Wait()
		if got := s.take(); len(got) != 0 || f.table.backlogged.Load() != 1 {
			t.Fatalf("frames %v backlogged %d", got, f.table.backlogged.Load())
		}
		s.refusing(acceptAll)
		f.route(msg("t1", 1))
		f.route(msg("t1", 1))
		if got := s.take(); !slices.Equal(got, []string{"load_failed"}) || backlogResyncs(f) != 0 || s.failures() != 0 {
			t.Fatalf("frames %v backlog resyncs %d failures %d", got, backlogResyncs(f), s.failures())
		}
		c.LoadRooms()
		synctest.Wait()
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		f.route(msg("t1", 1))
		if got := s.take(); !slices.Equal(got, []string{"ready", "event msg_created/t1/1//v0"}) || backlogResyncs(f) != 0 {
			t.Fatalf("after load_rooms frames %v backlog resyncs %d", got, backlogResyncs(f))
		}
	})
}

func TestASuspendedUserWhoseLoadFailedIsNeverSentABacklogResync(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(f.seq))
		synctest.Wait()
		s.refusing(refuseAll)
		f.loader.answer(1, reply{err: errors.New("core down")})
		synctest.Wait()
		if got := s.take(); len(got) != 0 || f.table.backlogged.Load() != 1 {
			t.Fatalf("frames %v backlogged %d", got, f.table.backlogged.Load())
		}
		s.refusing(acceptAll)
		for range 3 {
			f.route(msg("t1", 1))
		}
		if got := s.take(); !slices.Equal(got, []string{"load_failed"}) || backlogResyncs(f) != 0 || f.table.backlogged.Load() != 0 {
			t.Fatalf("frames %v backlog resyncs %d backlogged %d", got, backlogResyncs(f), f.table.backlogged.Load())
		}
	})
}
