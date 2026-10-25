package publish_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func readPositionsEvent(users int) (*chatimv1.Event, []store.StateTarget) {
	entries := make([]*chatimv1.ReadEntry, users)
	want := make([]store.StateTarget, users)
	for i := range users {
		u := "u" + strconv.Itoa(100_000+i)
		entries[i] = &chatimv1.ReadEntry{User: u, ReadSeq: uint64(i + 1), ReadVer: uint64(i + 2)}
		want[i] = store.StateTarget{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: roomA, User: u, Ver: uint64(i + 2)}
	}
	room := domain.Room{ID: roomA, Tenant: tenant, Type: domain.RoomGroup}
	return pbconv.ReadPositions(room, entries, sentAt), want
}

func TestAnAckedReadPositionsEventClearsEveryListedFlagInOneCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		ev, want := readPositionsEvent(3)
		if err := rg.Enqueue(roomA, []*chatimv1.Event{ev}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		closeRig(t, rg)
		if got := c.added(); !slices.Equal(got, want) {
			t.Fatalf("added = %+v, want %+v", got, want)
		}
		if n := c.callCount(); n != 1 {
			t.Fatalf("AddAll calls = %d, want 1", n)
		}
	})
}

func TestAFailedReadPositionsPublishClearsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c)).start(t)
		rg.js.Hold()
		ev, _ := readPositionsEvent(3)
		if err := rg.Enqueue(roomA, []*chatimv1.Event{ev}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		synctest.Wait()
		rg.js.Fail(errors.New("ack timeout"))
		closeRig(t, rg)
		if got := c.added(); len(got) != 0 {
			t.Fatalf("added = %+v, want none after a failed ack", got)
		}
	})
}

func TestTheAckFollowerHoldsOneSlotPerEventNotPerEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &recordingClears{}
		counters := &publish.Counters{}
		rg := newRig(t, fastSetup, publish.WithStateClears(c), publish.WithCounters(counters)).start(t)
		rg.js.Hold()
		ev, want := readPositionsEvent(2 * ackFollowQueue)
		if err := rg.Enqueue(roomA, []*chatimv1.Event{ev}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		rg.enqueue(t, roomA, seqs(1, ackFollowQueue)...)
		synctest.Wait()
		if got := counters.Drops().StateTrackFull; got != 0 {
			t.Fatalf("StateTrackFull = %d, want 0 for %d events", got, ackFollowQueue+1)
		}
		rg.js.Release()
		closeRig(t, rg)
		if got := len(c.added()); got != len(want)+ackFollowQueue {
			t.Fatalf("added %d targets, want %d", got, len(want)+ackFollowQueue)
		}
	})
}
