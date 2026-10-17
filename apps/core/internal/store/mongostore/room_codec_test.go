package mongostore

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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
	if got != r {
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
