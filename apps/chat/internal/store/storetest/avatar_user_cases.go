package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func avatarUserCases() []memberCase {
	return []memberCase{
		{"member sample gives up to the limit of active members of one room", memberSampleOfRoom},
		{"member sample rejects a bad room or limit", memberSampleInvalid},
		{"set avatar users writes when the avatar ver matches and bumps it", setAvatarUsersByVer},
		{"set avatar users rejects too many users", setAvatarUsersInvalid},
	}
}

func sample(t *testing.T, s MemberRooms, room uint64, limit int) []string {
	t.Helper()
	got, err := s.MemberSample(t.Context(), room, limit)
	if err != nil {
		t.Fatalf("MemberSample(%d, %d): %v", room, limit, err)
	}
	return got
}

func memberSampleOfRoom(t *testing.T, s MemberRooms) {
	seedRankedMembers(t, s)
	active := []string{"alice", "amy", "bea", "bob", "carl", "dave", "zed"}
	got := sample(t, s, roomA, 4)
	if len(got) != 4 || len(slices.Compact(slices.Sorted(slices.Values(got)))) != 4 {
		t.Fatalf("MemberSample(roomA, 4) = %v, want 4 different users", got)
	}
	for _, u := range got {
		if !slices.Contains(active, u) {
			t.Fatalf("MemberSample(roomA, 4) = %v, %s is not an active member", got, u)
		}
	}
	if got := slices.Sorted(slices.Values(sample(t, s, roomB, 4))); !slices.Equal(got, []string{"alice", "bob"}) {
		t.Fatalf("MemberSample(roomB, 4) = %v, want alice and bob", got)
	}
	if got := sample(t, s, roomC, 4); len(got) != 0 {
		t.Fatalf("MemberSample(empty room) = %v, want none", got)
	}
}

func memberSampleInvalid(t *testing.T, s MemberRooms) {
	for _, c := range []struct {
		room  uint64
		limit int
	}{{0, 4}, {1 << 63, 4}, {roomA, 0}, {roomA, domain.MaxAvatarUsers + 1}} {
		_, err := s.MemberSample(t.Context(), c.room, c.limit)
		assertErrorIs(t, "MemberSample", err, apperr.ErrInvalidArgument)
	}
}

func avatarOf(t *testing.T, s MemberRooms, room uint64) domain.Room {
	t.Helper()
	r, err := s.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get(%d): %v", room, err)
	}
	return r
}

func setAvatarUsersByVer(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	if err := s.SetAvatarUsers(t.Context(), roomA, 0, []string{"alice", "bob"}); err != nil {
		t.Fatalf("SetAvatarUsers(ver 0): %v", err)
	}
	if r := avatarOf(t, s, roomA); !slices.Equal(r.AvatarUsers, []string{"alice", "bob"}) || r.AvatarVer != 1 {
		t.Fatalf("after the first write: %v ver %d", r.AvatarUsers, r.AvatarVer)
	}
	assertErrorIs(t, "SetAvatarUsers(stale ver)", s.SetAvatarUsers(t.Context(), roomA, 0, []string{"bob"}), domain.ErrRetryLater)
	if err := s.SetAvatarUsers(t.Context(), roomA, 1, []string{"alice", "bob"}); err != nil {
		t.Fatalf("SetAvatarUsers(same users, ver 1): %v", err)
	}
	if err := s.SetAvatarUsers(t.Context(), roomA, 2, []string{}); err != nil {
		t.Fatalf("SetAvatarUsers(empty, ver 2): %v", err)
	}
	if r := avatarOf(t, s, roomA); len(r.AvatarUsers) != 0 || r.AvatarVer != 3 {
		t.Fatalf("after the empty write: %v ver %d", r.AvatarUsers, r.AvatarVer)
	}
	assertErrorIs(t, "SetAvatarUsers(no room)", s.SetAvatarUsers(t.Context(), roomC, 0, []string{"x"}), domain.ErrRetryLater)
}

func setAvatarUsersInvalid(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	err := s.SetAvatarUsers(t.Context(), roomA, 0, []string{"a", "b", "c", "d", "e"})
	assertErrorIs(t, "SetAvatarUsers(5 users)", err, apperr.ErrInvalidArgument)
	assertErrorIs(t, "SetAvatarUsers(room 0)", s.SetAvatarUsers(t.Context(), 0, 0, nil), apperr.ErrInvalidArgument)
}
