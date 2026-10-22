package publish_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	ackFollowQueue   = 4096
	ackFollowFullMsg = "ack follower queue full; flagged events are republished by the sweeper"
)

var _ publish.StateClears = (*stateclear.Queue)(nil)

type recordingClears struct {
	mu      sync.Mutex
	targets []store.StateTarget
	calls   int
}

func (c *recordingClears) AddAll(targets []store.StateTarget) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.targets = append(c.targets, targets...)
	c.calls++
}

func (c *recordingClears) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *recordingClears) added() []store.StateTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := slices.Clone(c.targets)
	slices.SortFunc(out, func(a, b store.StateTarget) int {
		return cmp.Or(cmp.Compare(a.Collection, b.Collection), cmp.Compare(a.Room, b.Room), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.User, b.User))
	})
	return out
}

func createdTarget(room, seq uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: room, Seq: seq}
}

func TestAckedEventsHandTheirStateToTheClearQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		rg.enqueue(t, roomA, 1, 2)
		rg.enqueue(t, roomB, 1)
		room := domain.Room{ID: roomA, Tenant: tenant, Type: domain.RoomGroup}
		if err := rg.Enqueue(roomA, []*chatimv1.Event{pbconv.ReadUpdated(room, "bob", domain.ReadPosition{Seq: 2, Ver: 3}, sentAt)}); err != nil {
			t.Fatalf("Enqueue(read_updated): %v", err)
		}
		closeRig(t, rg)
		want := []store.StateTarget{
			createdTarget(roomA, 1), createdTarget(roomA, 2), createdTarget(roomB, 1),
			{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: roomA, User: "bob", Ver: 3},
		}
		if got := c.added(); !slices.Equal(got, want) {
			t.Fatalf("added = %+v, want %+v", got, want)
		}
	})
}

func TestStateIsHandedOnlyAfterThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		if got := c.added(); len(got) != 0 {
			t.Fatalf("added before the ack = %+v, want none", got)
		}
		rg.js.Release()
		closeRig(t, rg)
		if got := c.added(); !slices.Equal(got, []store.StateTarget{createdTarget(roomA, 1)}) {
			t.Fatalf("added after the ack = %+v", got)
		}
	})
}

func TestFailedOrRefusedPublishesKeepTheirState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		rg.js.Fail(errors.New("ack timeout"))
		rg.js.RefuseWhen(func(*nats.Msg) error { return errRefused })
		rg.enqueue(t, roomA, 2)
		closeRig(t, rg)
		if got := c.added(); len(got) != 0 {
			t.Fatalf("added = %+v, want none for a failed ack and a refused publish", got)
		}
	})
}

func TestEventsWithoutAStateArePublishedAndNothingIsCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		m := domain.Message{Room: roomA, Seq: 7, Tenant: tenant, CreatedAt: sentAt, Reactions: domain.ReactionSummary{Version: 2}}
		views := pbconv.CountsChanged(domain.RoomGroup, m, sentAt)
		views.GetCountsChanged().Counter = "views"
		if err := rg.Enqueue(roomA, []*chatimv1.Event{views}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		closeRig(t, rg)
		if ids := storedIDs(rg.js); len(ids) != 1 {
			t.Fatalf("stored = %v, want the unmapped event published", ids)
		}
		if got := c.added(); len(got) != 0 {
			t.Fatalf("added = %+v, want none", got)
		}
	})
}

func TestFullAckFollowerDropsAndCountsWithoutBlockingPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		counters := &publish.Counters{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c), publish.WithCounters(counters)).start(t)
		rg.js.Hold()
		total := uint64(ackFollowQueue + 3)
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		rg.enqueue(t, roomA, seqs(2, total)...)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != int(total) {
			t.Fatalf("attempts while the follower is stuck = %d, want %d", got, total)
		}
		if got := counters.Drops().StateTrackFull; got != 2 {
			t.Fatalf("StateTrackFull %d; want 2 (one waiting, %d queued)", got, ackFollowQueue)
		}
		if got := rg.sink.Count(ackFollowFullMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", ackFollowFullMsg, got)
		}
		rg.js.Release()
		closeRig(t, rg)
		if got := len(c.added()); got != ackFollowQueue+1 {
			t.Fatalf("added %d; want %d", got, ackFollowQueue+1)
		}
	})
}

func TestCloseAbortsFollowersWaitingOnHeldAcks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := rg.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want DeadlineExceeded", err)
		}
		if err := rg.wait(); err != nil {
			t.Fatalf("Run = %v, want nil after Close", err)
		}
		rg.js.Release()
		if got := c.added(); len(got) != 0 {
			t.Fatalf("added = %+v, want none after the abort", got)
		}
	})
}
