package grpcsrv_test

import (
	"slices"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type readRow struct {
	seq, ver uint64
}

func TestReadPositionThroughTheService(t *testing.T) {
	events, reads := &recordingEvents{}, &recordingReads{}
	rg := newRig(t, options{events: events, reads: reads})
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
		{"read up to 2", read(2), readRow{2, 2}},
		{"read 0 means everything", read(0), readRow{3, 3}},
		{"read past the end is clamped", read(9), readRow{3, 3}},
		{"unread from 2", unread(2), readRow{1, 4}},
		{"read at the current position moves nothing", read(1), readRow{1, 4}},
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
	got := reads.list()
	want := []readAdd{
		{room: id, user: "bob", pos: domain.ReadPosition{Seq: 2, Ver: 2}},
		{room: id, user: "bob", pos: domain.ReadPosition{Seq: 3, Ver: 3}},
		{room: id, user: "bob", pos: domain.ReadPosition{Seq: 1, Ver: 4}},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("read sink adds %+v, want %+v", got, want)
	}
	if _, all := events.enqueued(); slices.ContainsFunc(all, func(ev *chatimv1.Event) bool { return ev.GetReadPositions() != nil }) {
		t.Fatal("a read command published read_positions itself; read positions go through the sink")
	}
}

type readAdd struct {
	room uint64
	user string
	pos  domain.ReadPosition
}

type recordingReads struct {
	mu   sync.Mutex
	adds []readAdd
}

func (r *recordingReads) Add(room domain.Room, user string, pos domain.ReadPosition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adds = append(r.adds, readAdd{room: room.ID, user: user, pos: pos})
}

func (r *recordingReads) list() []readAdd {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.adds)
}
