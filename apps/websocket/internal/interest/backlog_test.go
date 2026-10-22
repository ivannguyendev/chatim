package interest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func catchingUp(env feed.Envelope) feed.Envelope {
	env.CatchingUp = true
	return env
}

func backlogResyncs(f *fixture) uint64 {
	return f.table.stats.resyncs[chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG].Load()
}

func TestASlowDeviceWhileCatchingUpGetsOneResyncAndResumesOnceCaughtUp(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		slow, ss := f.connect("t1", "slow", 1)
		_, fs := f.connect("t1", "fast", 1)
		slow.Open("1")
		ss.take()
		fs.take()
		ss.refusing(refuseEvents)
		for range 3 {
			f.route(catchingUp(msg("t1", 1)))
			f.route(catchingUp(event("reaction_changed", "t1", 1, "", 0)))
		}
		sends, _ := ss.counts()
		if got := ss.take(); !slices.Equal(got, []string{"resync client_backlog"}) || ss.failures() != 0 || sends != 0 {
			t.Fatalf("slow device frames %v, failures %d, sends %d", got, ss.failures(), sends)
		}
		if got := fs.take(); len(got) != 3 {
			t.Fatalf("fast device got %v, want every message", got)
		}
		ss.refusing(acceptAll)
		f.route(catchingUp(msg("t1", 1)))
		if got := ss.take(); len(got) != 0 {
			t.Fatalf("the slow device received before the feed caught up: %v", got)
		}
		f.route(msg("t1", 1))
		f.route(event("reaction_changed", "t1", 1, "", 0))
		if got := ss.take(); !slices.Equal(got, []string{"event msg_created/t1/1//v0", "event reaction_changed/t1/1//v0"}) {
			t.Fatalf("after catching up the slow device got %v", got)
		}
		if backlogResyncs(f) != 1 || f.table.backlogged.Load() != 0 {
			t.Fatalf("backlog resyncs %d, still backlogged %d", backlogResyncs(f), f.table.backlogged.Load())
		}
	})
}

func TestAResyncThatDidNotFitIsSentOnceTheFeedCatchesUp(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, ss := f.connect("t1", "slow", 1)
		f.connect("t1", "other", 2)
		ss.take()
		ss.refusing(refuseAll)
		f.route(catchingUp(msg("t1", 1)))
		f.route(catchingUp(msg("t1", 1)))
		_, trySends := ss.counts()
		if got := ss.take(); len(got) != 0 || backlogResyncs(f) != 0 || trySends != 3 {
			t.Fatalf("frames %v resyncs %d try sends %d", got, backlogResyncs(f), trySends)
		}
		f.route(msg("t1", 2))
		sends, _ := ss.counts()
		if got := ss.take(); !slices.Equal(got, []string{"resync client_backlog"}) || sends != 1 || backlogResyncs(f) != 1 {
			t.Fatalf("after catching up frames %v sends %d", got, sends)
		}
		f.route(msg("t1", 1))
		if got := ss.take(); !slices.Equal(got, []string{"event msg_created/t1/1//v0"}) {
			t.Fatalf("live frames = %v", got)
		}
	})
}

func TestLiveEventsAlwaysUseTheBudgetedSend(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, s := f.connect("t1", "u", 1)
		s.take()
		s.refusing(refuseAll)
		f.route(msg("t1", 1))
		sends, trySends := s.counts()
		if got := s.take(); !slices.Equal(got, []string{"event msg_created/t1/1//v0"}) || sends != 1 || trySends != 0 {
			t.Fatalf("frames %v sends %d try sends %d", got, sends, trySends)
		}
	})
}

func TestABackloggedDeviceSkipsNotificationsWithoutResuming(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		pub := NewPublisher(f.table)
		_, s := f.connect("t1", "u", 1)
		s.take()
		s.refusing(refuseEvents)
		f.route(catchingUp(msg("t1", 1)))
		s.refusing(acceptAll)
		pub.Deliver(feed.Envelope{Tenant: "t1", Data: packet("t1", "mention", "u")})
		pub.Deliver(feed.Envelope{Tenant: "t1", Data: packet("t1", "mention", "u"), CatchingUp: true})
		if got := s.take(); !slices.Equal(got, []string{"resync client_backlog"}) || f.table.backlogged.Load() != 1 {
			t.Fatalf("a backlogged device got %v, backlogged %d", got, f.table.backlogged.Load())
		}
		f.route(msg("t1", 1))
		pub.Deliver(feed.Envelope{Tenant: "t1", Data: packet("t1", "mention", "u"), CatchingUp: true})
		if got := s.take(); !slices.Equal(got, []string{"event msg_created/t1/1//v0", "other"}) {
			t.Fatalf("after the events caught up the device got %v", got)
		}
	})
}

func TestAStaleMemberEventOfTheViewerItselfIsNotSentToItsOwnOpenRooms(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		x, xs := f.connect("t1", "x", 1)
		v, vs := f.connect("t1", "v", 1)
		x.Open("1")
		v.Open("1")
		f.route(added("t1", 1, "x", 3))
		xs.take()
		vs.take()
		f.route(removed("t1", 1, "x", 2))
		f.route(added("t1", 1, "x", 3))
		if got := xs.take(); len(got) != 0 {
			t.Fatalf("x saw its own stale member events as a viewer: %v", got)
		}
		if got := vs.take(); len(got) != 2 {
			t.Fatalf("another viewer got %v, want both events", got)
		}
		f.route(event("reaction_changed", "t1", 1, "", 0))
		if got := xs.take(); len(got) != 1 {
			t.Fatalf("x lost the room after stale events: %v", got)
		}
	})
}
