package pbconv_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var editedAt = sentAt.Add(time.Minute)

func TestMessageChangeEventIDAppendsTheVersion(t *testing.T) {
	cases := []struct {
		room, thread, seq uint64
		version           uint32
		want              string
	}{
		{42, 0, 7, 1, "42-0-7-v1"},
		{42, 3, 9, 12, "42-3-9-v12"},
		{1, 0, math.MaxUint64, math.MaxUint32, "1-0-18446744073709551615-v4294967295"},
	}
	for _, c := range cases {
		if got := pbconv.MessageChangeEventID(c.room, c.thread, c.seq, c.version); got != c.want {
			t.Errorf("MessageChangeEventID(%d, %d, %d, %d) = %q, want %q", c.room, c.thread, c.seq, c.version, got, c.want)
		}
	}
}

func TestMessageCarriesEditState(t *testing.T) {
	m := sample()
	m.Text, m.Version, m.Deleted, m.EditedAt, m.Hidden = "", 3, true, editedAt, true
	want := &chatimv1.Message{
		RoomId: "9007199254740993", Seq: 7, Sender: "alice", Kind: chatimv1.MessageKind_MESSAGE_KIND_TEXT,
		Cid: "c-1", CreatedAt: timestamppb.New(sentAt), Version: 3, Deleted: true, EditedAt: timestamppb.New(editedAt), Hidden: true,
	}
	if got := pbconv.Message(m); !proto.Equal(got, want) {
		t.Fatalf("Message = %v, want %v", got, want)
	}
	if got := pbconv.Message(sample()); got.GetEditedAt() != nil || got.GetVersion() != 0 {
		t.Fatalf("unedited message = %v, want no edited_at and version 0", got)
	}
}

func changeOf(kind domain.EditKind) (domain.Message, domain.Edit) {
	m := sample()
	m.Version, m.EditedAt = 2, editedAt
	e := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: kind, Tenant: "acme", By: "bob", At: editedAt}
	if kind == domain.EditDelete {
		m.Text, m.Deleted = "", true
		return m, e
	}
	m.Text, e.Text = "đã sửa", "đã sửa"
	return m, e
}

func changeEnvelope() *chatimv1.Event {
	return &chatimv1.Event{
		Id: "9007199254740993-0-7-v2", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Actor: "bob", Ts: timestamppb.New(editedAt),
	}
}

func TestMessageEditedEnvelope(t *testing.T) {
	m, e := changeOf(domain.EditText)
	want := changeEnvelope()
	want.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Message: pbconv.Message(m), Version: 2}}
	if got := pbconv.MessageEdited(domain.RoomGroup, m, e); !proto.Equal(got, want) {
		t.Fatalf("MessageEdited = %v, want %v", got, want)
	}
}

func TestMessageDeletedEnvelope(t *testing.T) {
	m, e := changeOf(domain.EditDelete)
	want := changeEnvelope()
	want.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Message: pbconv.Message(m), Version: 2}}
	got := pbconv.MessageDeleted(domain.RoomGroup, m, e)
	if !proto.Equal(got, want) {
		t.Fatalf("MessageDeleted = %v, want %v", got, want)
	}
	if text := got.GetMessageDeleted().GetMessage().GetText(); text != "" {
		t.Fatalf("deleted snapshot carries text %q", text)
	}
}

func TestMessageVersionsStartWithTheOriginalOnlyOnTheFirstPage(t *testing.T) {
	m := sample()
	v1 := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 1, Kind: domain.EditText, By: "alice", Text: "v1", Prev: "xin chào", At: editedAt}
	v2 := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: domain.EditDelete, By: "bob", At: editedAt.Add(time.Minute)}
	original := &chatimv1.MessageVersion{Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: "xin chào", By: "alice", At: timestamppb.New(sentAt)}
	first := &chatimv1.MessageVersion{Version: 1, Kind: chatimv1.EditKind_EDIT_KIND_TEXT, Text: "v1", By: "alice", At: timestamppb.New(editedAt)}
	second := &chatimv1.MessageVersion{Version: 2, Kind: chatimv1.EditKind_EDIT_KIND_DELETE, By: "bob", At: timestamppb.New(editedAt.Add(time.Minute))}
	cases := []struct {
		name  string
		edits []domain.Edit
		after uint32
		want  []*chatimv1.MessageVersion
	}{
		{"first page", []domain.Edit{v1, v2}, 0, []*chatimv1.MessageVersion{original, first, second}},
		{"next page", []domain.Edit{v2}, 1, []*chatimv1.MessageVersion{second}},
		{"never edited", nil, 0, nil},
		{"first page without version 1", []domain.Edit{v2}, 0, []*chatimv1.MessageVersion{second}},
	}
	for _, c := range cases {
		got := pbconv.MessageVersions(m, c.edits, c.after)
		if !slices.EqualFunc(got, c.want, func(a, b *chatimv1.MessageVersion) bool { return proto.Equal(a, b) }) {
			t.Errorf("%s: MessageVersions = %v, want %v", c.name, got, c.want)
		}
	}
}
