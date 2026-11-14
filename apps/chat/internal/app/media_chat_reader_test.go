package app

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/message"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/memstore"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	mediaRoom    = uint64(77)
	privateRoom  = uint64(88)
	mediaTenant  = "acme"
	mediaFileKey = "acme/alice/20261107/0123456789abcdefghijklmnop"
)

var mediaSent = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

type mediaFixture struct {
	node   *mediaChatNode
	reader *message.Reader
	rooms  *memstore.Rooms
	msgs   *memstore.Messages
	hidden *memstore.Hidden
}

func newMediaFixture(t *testing.T) mediaFixture {
	t.Helper()
	rooms, msgs, hidden := memstore.NewRooms(), memstore.NewMessages(), memstore.NewHidden()
	for _, r := range []struct {
		id    uint64
		users []string
	}{{mediaRoom, []string{"alice", "bob"}}, {privateRoom, []string{"alice"}}} {
		members := make([]domain.Member, len(r.users))
		for i, u := range r.users {
			members[i] = domain.Member{Room: r.id, Tenant: mediaTenant, User: u, Role: domain.RoleMember, JoinedAt: mediaSent.Add(-time.Hour)}
		}
		members[0].Role = domain.RoleOwner
		room := domain.Room{ID: r.id, Tenant: mediaTenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: mediaSent.Add(-time.Hour), MemberCount: len(r.users)}
		if err := rooms.Create(t.Context(), room, members); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	checker, err := access.NewChecker(rooms, access.DefaultPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := message.NewReader(message.ReaderDeps{Access: checker, Pages: msgs, Edits: memstore.NewEdits(), Hidden: hidden, Interactions: memstore.NewInteractions()})
	if err != nil {
		t.Fatal(err)
	}
	return mediaFixture{node: &mediaChatNode{mediaMessageFiles: mediaMessageFiles{messages: msgs}}, reader: reader, rooms: rooms, msgs: msgs, hidden: hidden}
}

func (f mediaFixture) insert(t *testing.T, room, thread, seq uint64, deleted bool, files ...domain.Attachment) {
	t.Helper()
	m := domain.Message{
		Room: room, Thread: thread, Seq: seq, Tenant: mediaTenant, From: "alice", Kind: domain.KindText, Text: "x",
		CID: "c", CreatedAt: mediaSent.Add(time.Duration(seq) * time.Second), Deleted: deleted, Attachments: files,
	}
	if res := f.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d: %+v", seq, res)
	}
}

func photo(name string) domain.Attachment {
	return domain.Attachment{Key: mediaFileKey, Name: name, Mime: "image/png", Size: 9, Dim: []int32{640, 480}, Hash: "sha256:ab"}
}

func ref(room string, thread, seq uint64) mediaapp.MessageRef {
	return mediaapp.MessageRef{Room: room, Thread: thread, Seq: seq}
}

func TestMediaChatNodeRefusesUntilTheReaderIsBound(t *testing.T) {
	f := newMediaFixture(t)
	if _, err := f.node.VisibleAttachments(t.Context(), mediaTenant, "bob", []mediaapp.MessageRef{ref("77", 0, 1)}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("unbound err = %v", err)
	}
}

func TestMediaChatNodeReturnsOnlyReadableMessagesWithFilesInRequestOrder(t *testing.T) {
	f := newMediaFixture(t)
	f.node.bind(f.reader)
	f.insert(t, mediaRoom, 0, 1, false, photo("one.png"))
	f.insert(t, mediaRoom, 0, 2, false)
	f.insert(t, mediaRoom, 0, 3, false, photo("hidden.png"))
	f.insert(t, mediaRoom, 0, 4, true, photo("deleted.png"))
	f.insert(t, mediaRoom, 0, 5, false, photo("five.png"), domain.Attachment{Key: mediaFileKey + "x", Name: "r.pdf", Mime: "application/pdf", Size: 3})
	f.insert(t, mediaRoom, 1, 1, false, photo("reply.png"))
	f.insert(t, privateRoom, 0, 1, false, photo("private.png"))
	if _, err := f.hidden.Hide(t.Context(), "bob", store.MsgKey{Room: mediaRoom, Seq: 3}, mediaSent); err != nil {
		t.Fatal(err)
	}
	refs := []mediaapp.MessageRef{
		ref("77", 0, 5), ref("88", 0, 1), ref("77", 0, 4), ref("77", 0, 3), ref("77", 0, 2),
		ref("77", 1, 1), ref("77", 0, 1), ref("77", 0, 5), ref("77", 0, 9),
	}
	got, err := f.node.VisibleAttachments(t.Context(), mediaTenant, "bob", refs)
	if err != nil {
		t.Fatal(err)
	}
	sentAt := func(seq int) time.Time { return mediaSent.Add(time.Duration(seq) * time.Second) }
	png := func(name string) mediaapp.File {
		return mediaapp.File{Key: mediaFileKey, Name: name, Mime: "image/png", Size: 9, Dim: []int32{640, 480}, Hash: "sha256:ab"}
	}
	want := []mediaapp.MessageFiles{
		{Ref: ref("77", 0, 5), Sender: "alice", SentAt: sentAt(5), Files: []mediaapp.File{png("five.png"), {Key: mediaFileKey + "x", Name: "r.pdf", Mime: "application/pdf", Size: 3, Dur: 0}}},
		{Ref: ref("77", 1, 1), Sender: "alice", SentAt: sentAt(1), Files: []mediaapp.File{png("reply.png")}},
		{Ref: ref("77", 0, 1), Sender: "alice", SentAt: sentAt(1), Files: []mediaapp.File{png("one.png")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	owner, err := f.node.VisibleAttachments(t.Context(), mediaTenant, "alice", []mediaapp.MessageRef{ref("88", 0, 1), ref("77", 0, 3)})
	if err != nil || len(owner) != 2 || owner[0].Ref != ref("88", 0, 1) {
		t.Fatalf("alice sees %+v, %v", owner, err)
	}
	if other, err := f.node.VisibleAttachments(t.Context(), "globex", "bob", refs); err != nil || len(other) != 0 {
		t.Fatalf("other tenant sees %+v, %v", other, err)
	}
}

func TestMediaChatNodeRejectsBadRefs(t *testing.T) {
	f := newMediaFixture(t)
	f.node.bind(f.reader)
	for _, bad := range []mediaapp.MessageRef{ref("", 0, 1), ref("abc", 0, 1), ref("0", 0, 1), ref("-7", 0, 1), ref("77", 0, 0)} {
		if _, err := f.node.VisibleAttachments(t.Context(), mediaTenant, "bob", []mediaapp.MessageRef{ref("77", 0, 1), bad}); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
}

func TestMediaChatNodeRoomReadAccessChecksMembershipAndGivesTheClearMark(t *testing.T) {
	f := newMediaFixture(t)
	if _, err := f.node.RoomReadAccess(t.Context(), mediaTenant, "bob", "77"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("unbound err = %v", err)
	}
	f.node.bind(f.reader)
	cleared := mediaSent.Add(time.Minute)
	if _, _, err := f.rooms.ClearHistory(t.Context(), mediaRoom, "bob", cleared); err != nil {
		t.Fatal(err)
	}
	got, err := f.node.RoomReadAccess(t.Context(), mediaTenant, "bob", "077")
	if err != nil || got.Room != "77" || !got.ClearedAt.Equal(cleared) {
		t.Fatalf("bob = %+v, %v", got, err)
	}
	if got, err := f.node.RoomReadAccess(t.Context(), mediaTenant, "alice", "77"); err != nil || !got.ClearedAt.IsZero() {
		t.Fatalf("alice = %+v, %v", got, err)
	}
	if _, err := f.node.RoomReadAccess(t.Context(), mediaTenant, "bob", "88"); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("outsider err = %v", err)
	}
	if _, err := f.node.RoomReadAccess(t.Context(), "globex", "bob", "77"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("other tenant err = %v", err)
	}
	for _, bad := range []string{"", "abc", "0", "-7"} {
		if _, err := f.node.RoomReadAccess(t.Context(), mediaTenant, "bob", bad); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("room %q err = %v", bad, err)
		}
	}
}
