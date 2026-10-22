package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestWSFeedRecordsEachReadPositionEntry(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Event(&chatimv1.Event{Id: "7-rp-x", RoomId: "7", Payload: &chatimv1.Event_ReadPositions{ReadPositions: &chatimv1.ReadPositions{
		Entries: []*chatimv1.ReadEntry{{User: "ann", ReadSeq: 4, ReadVer: 2}, {User: "bob", ReadSeq: 9, ReadVer: 5}},
	}}})
	if f.Len() != 2 {
		t.Fatalf("entries = %d, want one per reader", f.Len())
	}
	_, got, ok := f.Find(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "read_positions", Room: "7", User: "bob", Ver: 5})
	if !ok || got.Seq != 9 || got.ID != "7-rp-x" {
		t.Fatalf("bob's entry = %+v, %v", got, ok)
	}
	if _, _, ok := f.Find(0, e2e.WSMatch{Kind: "read_positions", User: "ann", Ver: 3}); ok {
		t.Fatal("an entry matched a read_ver it does not carry")
	}
}
