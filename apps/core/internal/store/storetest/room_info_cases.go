package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func roomInfoCases() []memberCase {
	return []memberCase{
		{"update room info writes on the base info ver only and bumps it", roomInfoCAS},
		{"update room info rejects an invalid change and writes nothing", roomInfoInvalid},
	}
}

func updateInfo(t *testing.T, s MemberRooms, base uint64, c store.RoomInfoChange) bool {
	t.Helper()
	ok, err := s.UpdateRoomInfo(t.Context(), roomA, base, c)
	if err != nil {
		t.Fatalf("UpdateRoomInfo(base %d, %+v): %v", base, c, err)
	}
	return ok
}

func roomInfoCAS(t *testing.T, s MemberRooms) {
	crew(t, s)
	want, err := s.Get(t.Context(), roomA)
	if err != nil || want.InfoVer != 1 {
		t.Fatalf("Get = %+v, %v; want info ver 1 at creation", want, err)
	}
	rename := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("Design"), Avatar: new("files/av/777.png")}, By: "alice", At: secs(5)}
	if !updateInfo(t, s, 1, rename) {
		t.Fatal("UpdateRoomInfo(base 1) = false, want written")
	}
	want.Name, want.Avatar, want.InfoVer, want.UpdatedBy = "Design", "files/av/777.png", 2, "alice"
	assertRoomInfo(t, s, want, secs(5))
	stale := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("Late")}, By: "bob", At: secs(6)}
	if updateInfo(t, s, 1, stale) {
		t.Fatal("UpdateRoomInfo(stale base 1) = true, want lost")
	}
	assertRoomInfo(t, s, want, secs(5))
	drop := store.RoomInfoChange{Update: domain.RoomUpdate{Avatar: new("")}, By: "bob", At: secs(7)}
	if !updateInfo(t, s, 2, drop) {
		t.Fatal("UpdateRoomInfo(base 2, drop avatar) = false, want written")
	}
	want.Avatar, want.InfoVer, want.UpdatedBy = "", 3, "bob"
	assertRoomInfo(t, s, want, secs(7))
	if ok, err := s.UpdateRoomInfo(t.Context(), roomB, 1, rename); ok || err != nil {
		t.Fatalf("UpdateRoomInfo(missing room) = %v, %v; want false, nil", ok, err)
	}
}

func assertRoomInfo(t *testing.T, s MemberRooms, want domain.Room, at time.Time) {
	t.Helper()
	got, err := s.Get(t.Context(), roomA)
	if err != nil || !got.UpdatedAt.Equal(at) {
		t.Fatalf("Get = %+v, %v; want updated at %v", got, err, at)
	}
	got.UpdatedAt, want.UpdatedAt = time.Time{}, time.Time{}
	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	if got != want {
		t.Fatalf("Get = %+v,\nwant %+v", got, want)
	}
}

func roomInfoInvalid(t *testing.T, s MemberRooms) {
	crew(t, s)
	good := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("Design")}, By: "alice", At: secs(5)}
	empty, badBy, noTime, badName := good, good, good, good
	empty.Update = domain.RoomUpdate{}
	badBy.By = "a b"
	noTime.At = time.Time{}
	badName.Update.Name = new("")
	for name, c := range map[string]store.RoomInfoChange{"empty": empty, "bad by": badBy, "zero time": noTime, "empty name": badName} {
		_, err := s.UpdateRoomInfo(t.Context(), roomA, 1, c)
		assertErrorIs(t, "UpdateRoomInfo("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, err := s.UpdateRoomInfo(t.Context(), 0, 1, good)
	assertErrorIs(t, "UpdateRoomInfo(room 0)", err, apperr.ErrInvalidArgument)
	if got, err := s.Get(t.Context(), roomA); err != nil || got.InfoVer != 1 || got.Name != "Team" {
		t.Fatalf("Get after rejected updates = %+v, %v; want Team at info ver 1", got, err)
	}
}
