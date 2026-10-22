package interest

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestOpenRoomBeforeReadyWaitsForReady(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		sink := &fakeSink{}
		c := f.table.Register("t1", "u", sink)
		synctest.Wait()
		c.Open("1")
		c.Open("abc")
		c.Open("5")
		c.Open("6")
		c.Close("6")
		if got := sink.take(); len(got) != 0 {
			t.Fatalf("frames before ready = %v", got)
		}
		f.loader.replies <- reply{rooms: []Room{{ID: 1, Ver: 1}}}
		synctest.Wait()
		if got := sink.take(); !slices.Equal(got, []string{"ready", "closed abc not_member"}) {
			t.Fatalf("frames at ready = %v", got)
		}
		f.route(event("reaction_changed", "t1", 1, "", 0))
		if got := sink.take(); len(got) != 1 {
			t.Fatalf("open room did not receive reactions: %v", got)
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if got := sink.take(); !slices.Equal(got, []string{"closed 5 not_member"}) {
			t.Fatalf("frames after the open wait = %v", got)
		}
		if got := f.table.stats.openRejected.Load(); got != 2 {
			t.Fatalf("open rejections = %d, want 2", got)
		}
	})
}

func TestOpenRoomRightAfterCreateRoomActivatesOnMemberAdded(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, sink := f.connect("t1", "u")
		sink.take()
		c.Open("9")
		time.Sleep(5 * time.Second)
		f.route(added("t1", 9, "u", 1))
		f.route(event("reaction_changed", "t1", 9, "", 0))
		time.Sleep(10 * time.Second)
		synctest.Wait()
		want := []string{"event member_added/t1/9/u/v1", "event reaction_changed/t1/9//v0"}
		if got := sink.take(); !slices.Equal(got, want) {
			t.Fatalf("frames = %v, want %v", got, want)
		}
		if got := f.table.stats.openRejected.Load(); got != 0 {
			t.Fatalf("open rejections = %d", got)
		}
	})
}

func TestOpenRoomOfANonMemberIsClosedAfterTheWait(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, sink := f.connect("t1", "u", 1)
		sink.take()
		c.Open("9")
		c.Open("9")
		time.Sleep(10*time.Second - time.Millisecond)
		synctest.Wait()
		if got := sink.take(); len(got) != 0 {
			t.Fatalf("closed before the wait ended: %v", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if got := sink.take(); !slices.Equal(got, []string{"closed 9 not_member"}) {
			t.Fatalf("frames = %v", got)
		}
		f.route(added("t2", 9, "u", 1))
		f.route(event("reaction_changed", "t1", 9, "", 0))
		if got := sink.take(); len(got) != 0 {
			t.Fatalf("a rejected room still receives: %v", got)
		}
	})
}

func TestCloseRoomAlwaysStopsTheRoom(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, sink := f.connect("t1", "u", 1)
		sink.take()
		c.Open("1")
		c.Open("1")
		f.route(event("reaction_changed", "t1", 1, "", 0))
		if got := sink.take(); len(got) != 1 {
			t.Fatalf("frames for a room opened twice = %v, want one", got)
		}
		c.Close("1")
		c.Close("1")
		c.Close("x")
		c.Close("2")
		f.route(event("reaction_changed", "t1", 1, "", 0))
		c.Open("8")
		c.Close("8")
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := sink.take(); len(got) != 0 {
			t.Fatalf("frames after close_room = %v", got)
		}
		f.route(msg("t1", 1))
		if got := sink.take(); len(got) != 1 {
			t.Fatalf("a closed room must still bring member-level events: %v", got)
		}
	})
}

func TestRemovedUserStopsReceivingAndGetsRoomClosed(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		open, openSink := f.connect("t1", "u", 1)
		_, idleSink := f.connect("t1", "u")
		open.Open("1")
		open.Open("4")
		openSink.take()
		idleSink.take()
		f.route(removed("t1", 1, "u", 2))
		f.route(removed("t1", 4, "u", 3))
		if got := openSink.take(); !slices.Equal(got, []string{
			"event member_removed/t1/1/u/v2", "closed 1 removed", "event member_removed/t1/4/u/v3", "closed 4 removed",
		}) {
			t.Fatalf("open connection frames = %v", got)
		}
		if got := idleSink.take(); !slices.Equal(got, []string{"event member_removed/t1/1/u/v2", "event member_removed/t1/4/u/v3"}) {
			t.Fatalf("other connection frames = %v", got)
		}
		f.route(msg("t1", 1))
		f.route(event("reaction_changed", "t1", 1, "", 0))
		f.route(added("t1", 4, "u", 1))
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := append(openSink.take(), idleSink.take()...); len(got) != 0 {
			t.Fatalf("frames after removal = %v", got)
		}
	})
}
