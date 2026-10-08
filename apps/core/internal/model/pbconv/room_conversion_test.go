package pbconv_test

import (
	"errors"
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRoomCopiesEveryField(t *testing.T) {
	room := domain.Room{ID: 9_007_199_254_740_993, Tenant: "acme", Type: domain.RoomDM, Name: "", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 2}
	want := &chatimv1.Room{
		Id: "9007199254740993", Tenant: "acme", Type: chatimv1.RoomType_ROOM_TYPE_DM, CreatedBy: "alice",
		CreatedAt: timestamppb.New(sentAt), MemberCount: 2,
	}
	if got := pbconv.Room(room); !proto.Equal(got, want) {
		t.Fatalf("Room = %v, want %v", got, want)
	}
}

func TestRoomClampsMemberCountToInt32(t *testing.T) {
	cases := []struct {
		in   int
		want int32
	}{
		{0, 0},
		{5000, 5000},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
		{-1, 0},
	}
	for _, c := range cases {
		if got := pbconv.Room(domain.Room{MemberCount: c.in}).GetMemberCount(); got != c.want {
			t.Errorf("member count %d = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestDomainRoomTypeAcceptsOnlyKnownTypes(t *testing.T) {
	cases := []struct {
		in   chatimv1.RoomType
		want domain.RoomType
		ok   bool
	}{
		{chatimv1.RoomType_ROOM_TYPE_DM, domain.RoomDM, true},
		{chatimv1.RoomType_ROOM_TYPE_GROUP, domain.RoomGroup, true},
		{chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED, "", false},
		{chatimv1.RoomType(99), "", false},
		{chatimv1.RoomType(-1), "", false},
	}
	for _, c := range cases {
		got, err := pbconv.DomainRoomType(c.in)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("DomainRoomType(%v) = %q, %v; want %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
		if !c.ok && !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("DomainRoomType(%v) error %v is not ErrInvalidArgument", c.in, err)
		}
	}
	for _, typ := range []domain.RoomType{domain.RoomDM, domain.RoomGroup} {
		if back, err := pbconv.DomainRoomType(pbconv.RoomType(typ)); err != nil || back != typ {
			t.Errorf("round trip %q = %q, %v", typ, back, err)
		}
	}
}

func TestRoomCreatedEventIDIsTheDecimalRoomWithASuffix(t *testing.T) {
	cases := map[uint64]string{
		1:                     "1-created",
		9_007_199_254_740_993: "9007199254740993-created",
		math.MaxInt64:         "9223372036854775807-created",
	}
	for room, want := range cases {
		if got := pbconv.RoomCreatedEventID(room); got != want {
			t.Errorf("RoomCreatedEventID(%d) = %q, want %q", room, got, want)
		}
	}
}

func TestRoomCreatedEnvelope(t *testing.T) {
	room := domain.Room{ID: 9_007_199_254_740_993, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 3}
	want := &chatimv1.Event{
		Id: "9007199254740993-created", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Actor: "alice", Ts: timestamppb.New(sentAt),
		Payload: &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: pbconv.Room(room)}},
	}
	got := pbconv.RoomCreated(room)
	if !proto.Equal(got, want) {
		t.Fatalf("RoomCreated = %v, want %v", got, want)
	}
	if got.GetThreadRoot() != 0 || got.GetSeq() != 0 || !got.GetTs().AsTime().Equal(sentAt) {
		t.Fatalf("thread %d seq %d ts %v, want 0, 0 and %v", got.GetThreadRoot(), got.GetSeq(), got.GetTs().AsTime(), sentAt)
	}
}
