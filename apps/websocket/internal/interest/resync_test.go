package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func lagJump(seq uint64) feed.Jump { return feed.Jump{Reason: feed.JumpLagTooOld, Seq: seq} }

func paced(t *testing.T, perSecond int) *fixture {
	return newFixtureWith(t, Config{Inflight: 64, OpenPending: 10 * time.Second, ReloadRate: perSecond})
}

func resyncsOf(f *fixture, reason chatimv1.ResyncReason) uint64 {
	return f.table.stats.resyncs[reason].Load()
}

const (
	lagTooOld    = chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD
	consumerLost = chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST
)

func TestReloadsArePacedByTheRateAndAGoneUserDoesNotTakeATurn(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 2)
		conns := map[string]*Conn{}
		for _, u := range []string{"a", "b", "c", "d"} {
			conns[u], _ = f.connect("t1", u, 1)
		}
		f.table.Jumped(lagJump(10))
		synctest.Wait()
		reloaded := f.loader.callOrder()[4:]
		if len(reloaded) != 1 || f.table.stats.suspended.Load() != 3 {
			t.Fatalf("reloads at the jump = %v, suspended %d", reloaded, f.table.stats.suspended.Load())
		}
		for u, c := range conns {
			if u != reloaded[0] {
				c.Unregister()
				break
			}
		}
		if f.table.stats.suspended.Load() != 2 {
			t.Fatalf("suspended after a user left = %d", f.table.stats.suspended.Load())
		}
		for i, at := range []time.Duration{500*time.Millisecond - time.Nanosecond, time.Nanosecond, 500 * time.Millisecond, time.Hour} {
			time.Sleep(at)
			synctest.Wait()
			if want := []int{1, 2, 3, 3}[i]; len(f.loader.callOrder()[4:]) != want {
				t.Fatalf("step %d: reloads %v, want %d", i, f.loader.callOrder()[4:], want)
			}
		}
		if f.table.stats.suspended.Load() != 0 || f.table.stats.reloads.Load() != 3 {
			t.Fatalf("suspended %d reloads %d", f.table.stats.suspended.Load(), f.table.stats.reloads.Load())
		}
	})
}

func TestReloadsStartWithWaitingDevicesThenUsersWithOpenRooms(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		f.connect("t1", "idle", 1)
		viewer, _ := f.connect("t1", "viewer", 1)
		viewer.Open("1")
		f.loader.holding(true)
		f.table.Register("t1", "loading", &fakeSink{})
		synctest.Wait()
		f.table.Jumped(lagJump(4))
		for range 3 {
			synctest.Wait()
			time.Sleep(time.Second)
		}
		if got := f.loader.callOrder()[3:]; !slices.Equal(got, []string{"loading", "viewer", "idle"}) {
			t.Fatalf("reload order = %v", got)
		}
	})
}

func TestANewDeviceOfASuspendedUserMovesItToTheFrontOfTheQueue(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		for _, u := range []string{"a", "b", "c", "d"} {
			f.connect("t1", u, 1)
		}
		f.table.Jumped(lagJump(4))
		synctest.Wait()
		first := f.loader.callOrder()[4]
		f.table.resyncMu.Lock()
		queue := f.table.reloads.queue
		late := queue[len(queue)-1].key.user
		f.table.resyncMu.Unlock()
		f.table.Register("t1", late, &fakeSink{})
		time.Sleep(time.Second)
		synctest.Wait()
		if got := f.loader.callOrder()[4:]; len(got) != 2 || got[1] != late {
			t.Fatalf("reloads = %v, want %s right after %s", got, late, first)
		}
	})
}

func TestAFailedReloadKeepsTheConnectionsAndRequeuesTheUserAtTheBack(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		sinks := map[string]*fakeSink{}
		for _, u := range []string{"u", "v"} {
			_, sinks[u] = f.connect("t1", u, 1)
			sinks[u].take()
		}
		f.loader.holding(true)
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		first := f.loader.callOrder()[2]
		f.loader.answer(2, reply{err: errors.New("core down")})
		synctest.Wait()
		if sinks[first].failures() != 0 || f.table.stats.reloadFailures.Load() != 1 || f.table.stats.loadFailures.Load() != 0 || f.table.stats.suspended.Load() != 2 {
			t.Fatalf("failures %d reload failures %d suspended %d", sinks[first].failures(), f.table.stats.reloadFailures.Load(), f.table.stats.suspended.Load())
		}
		for call := 3; call <= 4; call++ {
			time.Sleep(time.Second)
			synctest.Wait()
			f.loader.answer(call, reply{rooms: []Room{{ID: 1, Ver: 1}}})
			synctest.Wait()
		}
		order := f.loader.callOrder()[2:]
		if len(order) != 3 || order[1] == first || order[2] != first {
			t.Fatalf("reload order = %v, want %s again last", order, first)
		}
		for u, s := range sinks {
			if got := s.take(); !slices.Equal(got, []string{"resync lag_too_old"}) || s.failures() != 0 {
				t.Fatalf("%s frames %v failures %d", u, got, s.failures())
			}
		}
		if f.table.stats.suspended.Load() != 0 || f.table.stats.users.Load() != 2 {
			t.Fatalf("suspended %d users %d", f.table.stats.suspended.Load(), f.table.stats.users.Load())
		}
	})
}

func TestASecondJumpMidQueueSuspendsEveryoneAgainAndKeepsTheFirstStart(t *testing.T) {
	run(t, func(t *testing.T) {
		f := paced(t, 1)
		for _, u := range []string{"u1", "u2", "u3"} {
			f.connect("t1", u, 1)
		}
		f.table.Jumped(lagJump(5))
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		f.table.Jumped(feed.Jump{Reason: feed.JumpConsumerLost, Seq: 9})
		synctest.Wait()
		if f.table.stats.suspended.Load() != 3 {
			t.Fatalf("suspended after the second jump = %d", f.table.stats.suspended.Load())
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		order := f.loader.callOrder()[3:]
		first := order[0]
		if len(order) != 4 || order[3] != first || order[1] == first || order[2] == first || order[1] == order[2] {
			t.Fatalf("reload order = %v, want the waiting users first and %s again last", order, first)
		}
		if resyncsOf(f, lagTooOld) != 1 || resyncsOf(f, consumerLost) != 3 {
			t.Fatalf("lag resyncs %d lost resyncs %d", resyncsOf(f, lagTooOld), resyncsOf(f, consumerLost))
		}
		h := f.table.stats.reload.read()
		if h.Count != 4 || h.Sum != 5.5 || f.table.stats.suspended.Load() != 0 {
			t.Fatalf("reload seconds count %d sum %v, want 4 and 0+1+2+2.5", h.Count, h.Sum)
		}
	})
}

func TestARecreatedStreamResetsTheMarksSoItsEventsApply(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.route(withSeq(msg("t1", 1), 500))
		_, s := f.connect("t1", "u", 1)
		s.take()
		f.loader.set("t1", "u", 1)
		f.table.Jumped(feed.Jump{Reason: feed.JumpConsumerLost, Seq: 3})
		synctest.Wait()
		f.route(withSeq(added("t1", 9, "u", 1), 4))
		f.route(withSeq(msg("t1", 9), 5))
		want := []string{"resync consumer_lost", "event member_added/t1/9/u/v1", "event msg_created/t1/9//v0"}
		if got := s.take(); !slices.Equal(got, want) {
			t.Fatalf("frames = %v, want %v", got, want)
		}
	})
}

func TestUsersSuspendedFallsToZeroWhenUsersLeaveOrTheTableCloses(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		a, _ := f.connect("t1", "a", 1)
		f.connect("t1", "b", 1)
		f.loader.holding(true)
		f.table.Jumped(lagJump(3))
		synctest.Wait()
		a.Unregister()
		if f.table.stats.suspended.Load() != 1 {
			t.Fatalf("suspended after a left = %d", f.table.stats.suspended.Load())
		}
		f.table.Close()
		if f.table.stats.suspended.Load() != 0 {
			t.Fatalf("suspended after close = %d", f.table.stats.suspended.Load())
		}
	})
}
