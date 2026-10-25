package e2e_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const removedRoom = "7"

func removedCreated(seq uint64) e2e.WSEntry {
	return e2e.WSEntry{Frame: e2e.FrameEvent, Kind: "message_created", Room: removedRoom, Seq: seq}
}

func TestCheckRemovedAcceptsMemberRemovedThenRoomClosedThenSilence(t *testing.T) {
	entries := []e2e.WSEntry{
		removedCreated(9), removedCreated(10),
		{Frame: e2e.FrameEvent, Kind: "member_removed", Room: removedRoom, User: "l"},
		{Frame: e2e.FrameRoomClosed, Kind: "ROOM_CLOSED_REASON_REMOVED", Room: removedRoom},
		{Frame: e2e.FrameEvent, Kind: "message_created", Room: "8", Seq: 11},
	}
	c := e2e.CheckRemoved(entries, removedRoom, "l", 10)
	if err := c.Err(); err != nil || !c.Removed || c.Resynced {
		t.Fatalf("check = %+v, err %v; want removed, not resynced, no error", c, err)
	}
}

func TestCheckRemovedAcceptsAResyncInsteadOfMemberRemoved(t *testing.T) {
	entries := []e2e.WSEntry{removedCreated(9), {Frame: e2e.FrameResync, Kind: "RESYNC_REASON_LAG_TOO_OLD"}}
	c := e2e.CheckRemoved(entries, removedRoom, "l", 10)
	if err := c.Err(); err != nil || c.Removed || !c.Resynced {
		t.Fatalf("check = %+v, err %v; want resynced, no error", c, err)
	}
}

func TestCheckRemovedRefusesMessagesSentAfterTheRemoval(t *testing.T) {
	entries := []e2e.WSEntry{removedCreated(11), {Frame: e2e.FrameResync}, removedCreated(12)}
	c := e2e.CheckRemoved(entries, removedRoom, "l", 10)
	if c.Err() == nil || !slices.Equal(c.Late, []uint64{11, 12}) {
		t.Fatalf("check = %+v, err %v; want late seqs 11 and 12", c, c.Err())
	}
}

func TestCheckRemovedRefusesRoomEventsAfterTheUserWasTold(t *testing.T) {
	entries := []e2e.WSEntry{
		{Frame: e2e.FrameResync},
		{Frame: e2e.FrameEvent, Kind: "reaction_changed", Room: removedRoom, Seq: 3},
	}
	c := e2e.CheckRemoved(entries, removedRoom, "l", 10)
	if c.Err() == nil || c.After != 1 {
		t.Fatalf("check = %+v, err %v; want one room event after the resync", c, c.Err())
	}
}

func TestCheckRemovedRefusesAUserNeverTold(t *testing.T) {
	c := e2e.CheckRemoved([]e2e.WSEntry{removedCreated(9)}, removedRoom, "l", 10)
	if c.Err() == nil {
		t.Fatalf("check = %+v; want an error when neither member_removed nor resync arrived", c)
	}
}
