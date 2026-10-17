package grpcsrv_test

import (
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type pinSeen struct {
	seq, pinVer    uint64
	by, text       string
	hidden, delete bool
}

func TestListPinsShowsTheNewestPinFirstWithPlaceholdersPerViewer(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for seq, text := range map[uint64]string{1: "one", 2: "two", 3: "three", 4: "four"} {
		rg.insertAt(t, room, seq, text, sentAtSeq(seq))
	}
	for _, p := range []struct {
		seq  uint64
		user string
	}{{1, "alice"}, {2, "bob"}, {3, "alice"}, {4, "bob"}} {
		if _, err := rg.client.PinMessage(as(t, "acme", p.user), &chatimv1.PinMessageRequest{RoomId: room, Seq: p.seq}); err != nil {
			t.Fatalf("PinMessage(%d): %v", p.seq, err)
		}
	}
	if _, err := rg.client.UnpinMessage(alice, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 3}); err != nil {
		t.Fatalf("UnpinMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	resp, err := rg.client.ListPins(bob, &chatimv1.ListPinsRequest{RoomId: room})
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	var got []pinSeen
	for _, it := range resp.GetItems() {
		m := it.GetMessage()
		got = append(got, pinSeen{it.GetPin().GetSeq(), it.GetPin().GetPinVer(), it.GetPin().GetBy(), m.GetText(), m.GetHidden(), m.GetDeleted()})
		if m.GetSeq() != it.GetPin().GetSeq() {
			t.Fatalf("item pins %d but carries message %d", it.GetPin().GetSeq(), m.GetSeq())
		}
	}
	want := []pinSeen{{4, 4, "bob", "four", false, false}, {2, 2, "bob", "", true, false}, {1, 1, "alice", "", false, true}}
	if resp.GetPinVer() != 5 || !slices.Equal(got, want) {
		t.Fatalf("ListPins = ver %d %+v, want ver 5 %+v", resp.GetPinVer(), got, want)
	}
}

func TestListPinsCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	_, err := rg.client.ListPins(as(t, "acme", "carol"), &chatimv1.ListPinsRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	empty, err := rg.client.ListPins(as(t, "acme", "bob"), &chatimv1.ListPinsRequest{RoomId: room})
	if err != nil || empty.GetPinVer() != 0 || len(empty.GetItems()) != 0 {
		t.Fatalf("ListPins(no pins) = %v, %v; want version 0 and no items", empty, err)
	}
}
