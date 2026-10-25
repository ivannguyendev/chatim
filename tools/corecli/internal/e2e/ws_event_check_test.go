package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func createdIn(room string, thread, seq uint64, id string) *chatimv1.Event {
	return &chatimv1.Event{Id: id, RoomId: room, ThreadRoot: thread, Seq: seq, Payload: &chatimv1.Event_MessageCreated{
		MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{RoomId: room, Seq: seq}},
	}}
}

func TestEventCheckDropsARepeatedEventID(t *testing.T) {
	c := e2e.NewEventCheck()
	if fresh, _ := c.Apply(memberAdded("7", "bob")); !fresh {
		t.Fatal("the first member_added was not fresh")
	}
	if fresh, _ := c.Apply(memberAdded("7", "bob")); fresh {
		t.Fatal("a repeated event id was fresh")
	}
	if fresh, _ := c.Apply(&chatimv1.Event{RoomId: "7"}); !fresh {
		t.Fatal("an event without id was dropped")
	}
	if s := c.Stats(); s.Delivered != 2 || s.Duplicates != 1 || s.Gaps != 0 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestEventCheckDropsASeqAlreadySeenUnderAnotherID(t *testing.T) {
	c := e2e.NewEventCheck()
	c.Apply(createdIn("7", 0, 1, "a"))
	if fresh, _ := c.Apply(createdIn("7", 0, 1, "b")); fresh {
		t.Fatal("seq 1 delivered twice")
	}
	if fresh, _ := c.Apply(createdIn("7", 5, 1, "c")); !fresh {
		t.Fatal("seq 1 of another thread was dropped")
	}
	if s := c.Stats(); s.Duplicates != 1 || s.Delivered != 2 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestEventCheckReportsAGapPerTimelineAndNoneForALateSeq(t *testing.T) {
	c := e2e.NewEventCheck()
	if _, gap := c.Apply(createdIn("7", 0, 4, "")); !gap.Empty() {
		t.Fatalf("the first seq seen reported gap %+v", gap)
	}
	if _, gap := c.Apply(createdIn("7", 0, 5, "")); !gap.Empty() {
		t.Fatalf("the next seq reported gap %+v", gap)
	}
	_, gap := c.Apply(createdIn("7", 0, 9, ""))
	if gap != (e2e.Gap{Room: "7", From: 6, To: 8}) {
		t.Fatalf("gap = %+v, want 6..8 of room 7", gap)
	}
	if fresh, gap := c.Apply(createdIn("7", 0, 7, "")); !fresh || !gap.Empty() {
		t.Fatalf("a late seq = %v, %+v, want fresh with no gap", fresh, gap)
	}
	if _, gap := c.Apply(createdIn("8", 0, 3, "")); !gap.Empty() {
		t.Fatalf("another room reported gap %+v", gap)
	}
	if s := c.Stats(); s.Gaps != 1 || s.Delivered != 5 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestCheckEventStatsRefusesDuplicatesAndGaps(t *testing.T) {
	if err := e2e.CheckEventStats("b", e2e.EventStats{Delivered: 9}); err != nil {
		t.Fatalf("clean stats = %v", err)
	}
	for _, s := range []e2e.EventStats{{Duplicates: 1}, {Gaps: 1}} {
		if err := e2e.CheckEventStats("b", s); err == nil {
			t.Fatalf("stats %+v accepted", s)
		}
	}
}
