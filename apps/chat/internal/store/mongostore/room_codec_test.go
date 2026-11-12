package mongostore

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

func TestRoomCodecRoundTripsInfoFields(t *testing.T) {
	r := domain.Room{
		ID: 7_340_000_001, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime,
		MemberCount: 2, MemberCountVer: 3, Avatar: "files/av/777.png", InfoVer: 4, UpdatedAt: codecTime.Add(time.Minute), UpdatedBy: "minh",
	}
	doc, err := encodeRoom(r)
	if err != nil {
		t.Fatalf("encodeRoom: %v", err)
	}
	back, raw := roundTrip(t, doc)
	want := []string{"_id", "tenant", "type", "name", "created_by", "created_at", "member_count", "member_count_ver", "avatar", "info_ver", "updated_at", "updated_by"}
	if got := fieldNames(t, raw); !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeRoom(back)
	if err != nil || !got.CreatedAt.Equal(r.CreatedAt) || !got.UpdatedAt.Equal(r.UpdatedAt) {
		t.Fatalf("decodeRoom = %+v, %v; want %+v", got, err, r)
	}
	got.CreatedAt, got.UpdatedAt = r.CreatedAt, r.UpdatedAt
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("decoded %+v, want %+v", got, r)
	}
}

func TestRoomCodecDecodesTheLastEditTime(t *testing.T) {
	d := roomDoc{ID: 7_340_000_001, Tenant: "acme", Type: domain.RoomGroup, LastEditAt: codecTime.Add(time.Hour)}
	got, err := decodeRoom(d)
	if err != nil || !got.LastEditAt.Equal(d.LastEditAt) {
		t.Fatalf("decodeRoom = %+v, %v; want last edit at %v", got, err, d.LastEditAt)
	}
	d.InfoVer = -1
	if _, err := decodeRoom(d); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative info ver) = %v, want errCorrupt", err)
	}
}

func TestRoomCodecCarriesTheAvatarUsers(t *testing.T) {
	r := domain.Room{ID: 7_340_000_002, Tenant: "acme", Type: domain.RoomGroup, AvatarUsers: []string{"bob", "carl"}, AvatarVer: 3}
	doc, err := encodeRoom(r)
	if err != nil {
		t.Fatal(err)
	}
	back, raw := roundTrip(t, doc)
	if got := fieldNames(t, raw); !slices.Contains(got, "avatar_users") || !slices.Contains(got, "avatar_ver") {
		t.Fatalf("fields = %v", got)
	}
	got, err := decodeRoom(back)
	if err != nil || !slices.Equal(got.AvatarUsers, r.AvatarUsers) || got.AvatarVer != 3 {
		t.Fatalf("decodeRoom = %+v, %v", got, err)
	}
	back.AvatarVer = -1
	if _, err := decodeRoom(back); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative avatar ver) = %v, want errCorrupt", err)
	}
}
