package storetest

import (
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const listMissingRoom uint64 = 7_340_000_099

func roomListReadCases() []roomListCase {
	return []roomListCase{
		{"members by keys returns whole docs in key order, tombstones too, and skips missing keys", membersByKeys},
		{"room activities follow the asked ids and skip missing rooms", roomActivities},
		{"room infos follow the asked ids, skip missing rooms and carry the info fields", roomInfos},
		{"count rooms counts only active rooms of the user in the tenant", countRoomsActive},
		{"room list reads reject invalid input", roomListInvalid},
	}
}

func listPair(t *testing.T, s ListableRooms) {
	t.Helper()
	for _, id := range []uint64{roomA, roomB} {
		room, members := teamOf(id)
		mustCreate(t, s, room, members)
	}
}

func membersByKeys(t *testing.T, s ListableRooms) {
	listPair(t, s)
	removeMember(t, s, docOf(t, s, roomB, "bob"), secs(5))
	keys := []store.MemberKey{{Room: roomA, User: "bob"}, {Room: roomA, User: "nobody"}, {Room: roomB, User: "bob"}, {Room: listMissingRoom, User: "alice"}, {Room: roomA, User: "alice"}}
	got, err := s.MembersByKeys(t.Context(), keys)
	if err != nil {
		t.Fatalf("MembersByKeys: %v", err)
	}
	assertMembers(t, "MembersByKeys", got, []domain.Member{docOf(t, s, roomA, "bob"), docOf(t, s, roomB, "bob"), docOf(t, s, roomA, "alice")})
	if got, err := s.MembersByKeys(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("MembersByKeys(none) = %+v, %v; want none", got, err)
	}
}

func roomActivities(t *testing.T, s ListableRooms) {
	listPair(t, s)
	acts := []store.Activity{{Room: roomA, Seq: 9, At: secs(20)}, {Room: roomA, At: secs(30), Edited: true}}
	if err := s.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	got, err := s.RoomActivities(t.Context(), []uint64{roomB, listMissingRoom, roomA})
	if err != nil {
		t.Fatalf("RoomActivities: %v", err)
	}
	want := []store.RoomActivity{{Room: roomB}, {Room: roomA, LastSeq: 9, LastMessageAt: secs(20), LastEditAt: secs(30)}}
	if !slices.EqualFunc(got, want, sameActivity) {
		t.Fatalf("RoomActivities = %+v, want %+v", got, want)
	}
	if got, err := s.RoomActivities(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("RoomActivities(none) = %+v, %v; want none", got, err)
	}
}

func roomInfos(t *testing.T, s ListableRooms) {
	listPair(t, s)
	name := "Renamed"
	if ok, err := s.UpdateRoomInfo(t.Context(), roomB, 1, store.RoomInfoChange{Update: domain.RoomUpdate{Name: &name}, By: "alice", At: secs(40)}); err != nil || !ok {
		t.Fatalf("UpdateRoomInfo = %v, %v; want written", ok, err)
	}
	got, err := s.RoomInfos(t.Context(), []uint64{roomB, listMissingRoom, roomA})
	if err != nil {
		t.Fatalf("RoomInfos: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("RoomInfos = %+v, want rooms B and A", got)
	}
	for i, want := range []struct {
		id      uint64
		name    string
		infoVer uint64
	}{{roomB, name, 2}, {roomA, "Team", 1}} {
		r := got[i]
		if r.ID != want.id || r.Name != want.name || r.InfoVer != want.infoVer || r.Tenant != tenant || r.Type != domain.RoomGroup || r.MemberCount != 2 {
			t.Fatalf("RoomInfos[%d] = %+v, want room %d named %q at info ver %d with 2 members", i, r, want.id, want.name, want.infoVer)
		}
	}
}

func countRoomsActive(t *testing.T, s ListableRooms) {
	listPair(t, s)
	removeMember(t, s, docOf(t, s, roomB, "bob"), secs(5))
	for user, want := range map[string]int{"alice": 2, "bob": 1, "nobody": 0} {
		if got, err := s.CountRooms(t.Context(), tenant, user, 10); err != nil || got != want {
			t.Fatalf("CountRooms(%q) = %d, %v; want %d", user, got, err, want)
		}
	}
	if got, err := s.CountRooms(t.Context(), "globex", "alice", 10); err != nil || got != 0 {
		t.Fatalf("CountRooms(globex, alice) = %d, %v; want 0", got, err)
	}
}

func roomListInvalid(t *testing.T, s ListableRooms) {
	listPair(t, s)
	tooMany := make([]uint64, store.RoomActivityBatch+1)
	for i := range tooMany {
		tooMany[i] = uint64(i + 1)
	}
	manyKeys := make([]store.MemberKey, store.MaxMemberScan+1)
	for i := range manyKeys {
		manyKeys[i] = store.MemberKey{Room: roomA, User: "alice"}
	}
	good := store.RoomListQuery{Tenant: tenant, User: "alice", Folder: domain.FolderMain}
	badFolder, badUser := good, good
	badFolder.Folder, badUser.User = 0, "a b"
	calls := map[string]func() error{
		"count limit 0":       func() error { _, err := s.CountRooms(t.Context(), tenant, "alice", 0); return err },
		"count bad user":      func() error { _, err := s.CountRooms(t.Context(), tenant, "", 5); return err },
		"joined bad folder":   func() error { _, err := s.JoinedRoomOrder(t.Context(), badFolder); return err },
		"joined bad user":     func() error { _, err := s.JoinedRoomOrder(t.Context(), badUser); return err },
		"stepwise bad folder": func() error { _, err := s.StepwiseRoomOrder(t.Context(), badFolder); return err },
		"stepwise bad user":   func() error { _, err := s.StepwiseRoomOrder(t.Context(), badUser); return err },
		"activities too many": func() error { _, err := s.RoomActivities(t.Context(), tooMany); return err },
		"activities room 0":   func() error { _, err := s.RoomActivities(t.Context(), []uint64{roomA, 0}); return err },
		"infos too many":      func() error { _, err := s.RoomInfos(t.Context(), tooMany); return err },
		"infos huge room":     func() error { _, err := s.RoomInfos(t.Context(), []uint64{math.MaxUint64}); return err },
		"keys too many":       func() error { _, err := s.MembersByKeys(t.Context(), manyKeys); return err },
		"keys bad user": func() error {
			_, err := s.MembersByKeys(t.Context(), []store.MemberKey{{Room: roomA, User: ""}})
			return err
		},
		"keys room 0": func() error { _, err := s.MembersByKeys(t.Context(), []store.MemberKey{{User: "alice"}}); return err },
	}
	for name, call := range calls {
		assertErrorIs(t, name, call(), apperr.ErrInvalidArgument)
	}
}
