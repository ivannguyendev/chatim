package pbconv_test

import (
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var sentAt = time.UnixMilli(1_700_000_000_123).UTC()

func sample() domain.Message {
	return domain.Message{Room: 9_007_199_254_740_993, Seq: 7, Pts: 7, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "xin chào", CID: "c-1", CreatedAt: sentAt}
}

func TestIdentifiersAreDecimal(t *testing.T) {
	cases := []struct {
		name      string
		got, want string
	}{
		{"room", pbconv.RoomID(9_007_199_254_740_993), "9007199254740993"},
		{"max room", pbconv.RoomID(math.MaxInt64), "9223372036854775807"},
		{"event", pbconv.EventID(42, 7), "42-7"},
		{"event max pts", pbconv.EventID(1, math.MaxUint64), "1-18446744073709551615"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestEnumsMapKnownValuesAndDefaultToUnspecified(t *testing.T) {
	rooms := []struct {
		in   domain.RoomType
		want chatimv1.RoomType
	}{
		{domain.RoomDM, chatimv1.RoomType_ROOM_TYPE_DM},
		{domain.RoomGroup, chatimv1.RoomType_ROOM_TYPE_GROUP},
		{"channel", chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED},
		{"", chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED},
	}
	for _, c := range rooms {
		if got := pbconv.RoomType(c.in); got != c.want {
			t.Errorf("RoomType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	kinds := []struct {
		in   domain.Kind
		want chatimv1.MessageKind
	}{
		{domain.KindText, chatimv1.MessageKind_MESSAGE_KIND_TEXT},
		{0, chatimv1.MessageKind_MESSAGE_KIND_UNSPECIFIED},
		{99, chatimv1.MessageKind_MESSAGE_KIND_UNSPECIFIED},
	}
	for _, c := range kinds {
		if got := pbconv.MessageKind(c.in); got != c.want {
			t.Errorf("MessageKind(%d) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMessageCopiesEveryField(t *testing.T) {
	want := &chatimv1.Message{
		RoomId: "9007199254740993", Seq: 7, Pts: 7, Sender: "alice", Kind: chatimv1.MessageKind_MESSAGE_KIND_TEXT,
		Text: "xin chào", Cid: "c-1", CreatedAt: timestamppb.New(sentAt),
	}
	if got := pbconv.Message(sample()); !proto.Equal(got, want) {
		t.Fatalf("Message = %v, want %v", got, want)
	}
}

func TestMessageCreatedEnvelope(t *testing.T) {
	m := sample()
	want := &chatimv1.Event{
		Id: "9007199254740993-7", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Pts: 7, Actor: "alice", Ts: timestamppb.New(sentAt),
		Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: pbconv.Message(m)}},
	}
	got := pbconv.MessageCreated(domain.RoomGroup, m)
	if !proto.Equal(got, want) {
		t.Fatalf("MessageCreated = %v, want %v", got, want)
	}
	if !got.GetTs().AsTime().Equal(sentAt) || got.GetThreadRoot() != 0 {
		t.Fatalf("ts %v thread %d, want %v and 0", got.GetTs().AsTime(), got.GetThreadRoot(), sentAt)
	}
}
