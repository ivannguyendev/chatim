package grpcsrv_test

import (
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type readRow struct {
	seq, ver uint64
}

func TestReadPositionThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for _, cid := range []string{"c-1", "c-2", "c-3"} {
		rg.send(t, alice, room, cid, "hi")
	}
	read := func(seq uint64) readRow {
		t.Helper()
		resp, err := rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: room, Seq: seq})
		if err != nil {
			t.Fatalf("MarkRead(%d): %v", seq, err)
		}
		return readRow{resp.GetReadSeq(), resp.GetReadVer()}
	}
	unread := func(seq uint64) readRow {
		t.Helper()
		resp, err := rg.client.MarkUnread(bob, &chatimv1.MarkUnreadRequest{RoomId: room, Seq: seq})
		if err != nil {
			t.Fatalf("MarkUnread(%d): %v", seq, err)
		}
		return readRow{resp.GetReadSeq(), resp.GetReadVer()}
	}
	steps := []struct {
		name string
		got  readRow
		want readRow
	}{
		{"read up to 2", read(2), readRow{2, 1}},
		{"read 0 means everything", read(0), readRow{3, 2}},
		{"read past the end is clamped", read(9), readRow{3, 2}},
		{"unread from 2", unread(2), readRow{1, 3}},
		{"read at the current position moves nothing", read(1), readRow{1, 3}},
	}
	for _, s := range steps {
		if s.got != s.want {
			t.Fatalf("%s = %+v, want %+v", s.name, s.got, s.want)
		}
	}
	_, err := rg.client.MarkUnread(bob, &chatimv1.MarkUnreadRequest{RoomId: room})
	expectCode(t, err, codes.InvalidArgument)
	_, err = rg.client.MarkRead(as(t, "acme", "mallory"), &chatimv1.MarkReadRequest{RoomId: room, Seq: 1})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: "x", Seq: 1})
	expectCode(t, err, codes.InvalidArgument)
	id := roomNumber(t, room)
	var got []string
	_, all := events.enqueued()
	for _, ev := range all {
		if ev.GetReadUpdated() != nil {
			if ev.GetActor() != "bob" || ev.GetReadUpdated().GetUser() != "bob" {
				t.Fatalf("read_updated %v, want bob as actor and reader", ev)
			}
			got = append(got, ev.GetId())
		}
	}
	want := []string{pbconv.ReadEventID(id, "bob", 1), pbconv.ReadEventID(id, "bob", 2), pbconv.ReadEventID(id, "bob", 3)}
	if !slices.Equal(got, want) {
		t.Fatalf("read_updated ids %v, want %v", got, want)
	}
}
