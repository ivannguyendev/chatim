package storetest

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

const (
	avatarRoom    = "9007199254740993"
	otherRoom     = "9007199254740995"
	avatarA       = "acme/an/pub/20261113/aaaa"
	avatarB       = "acme/an/pub/20261113/bbbb"
	foreignAvatar = "other/an/pub/20261113/aaaa"
)

type avatarCase struct {
	name string
	run  func(t *testing.T, s store.RoomAvatars)
}

func RunRoomAvatars(t *testing.T, open func(t *testing.T) store.RoomAvatars) {
	for _, c := range []avatarCase{
		{"the first put inserts and get reads it back", putFirstAvatar},
		{"a put on the current ver replaces the whole doc", putReplacesTheDoc},
		{"a put on a stale ver or a second first put is a retry", putOnAStaleVer},
		{"in use sees every room holding the key, the one just written too", inUseSeesEveryRoom},
		{"a key of another tenant with the same tail is not in use", inUseIgnoresOtherTenants},
		{"a put without an info ver is refused", putWithoutAVer},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func RoomAvatar(room, tenant, key string, ver uint64) domain.RoomAvatar {
	return domain.RoomAvatar{Room: room, Tenant: tenant, FileKey: key, InfoVer: ver, UpdatedAt: created}
}

func putAvatar(t *testing.T, s store.RoomAvatars, next domain.RoomAvatar, prev uint64) {
	t.Helper()
	if err := s.Put(t.Context(), next, prev); err != nil {
		t.Fatalf("Put(%+v, %d) = %v", next, prev, err)
	}
}

func assertAvatar(t *testing.T, s store.RoomAvatars, want domain.RoomAvatar) {
	t.Helper()
	got, found, err := s.Get(t.Context(), want.Room)
	if err != nil || !found || got != want {
		t.Fatalf("Get = %+v, %v, %v; want %+v", got, found, err, want)
	}
}

func assertInUse(t *testing.T, s store.RoomAvatars, key string, want bool) {
	t.Helper()
	if got, err := s.InUse(t.Context(), key); err != nil || got != want {
		t.Fatalf("InUse(%s) = %v, %v; want %v", key, got, err, want)
	}
}

func putFirstAvatar(t *testing.T, s store.RoomAvatars) {
	if got, found, err := s.Get(t.Context(), avatarRoom); err != nil || found {
		t.Fatalf("Get before any put = %+v, %v, %v", got, found, err)
	}
	first := RoomAvatar(avatarRoom, "acme", avatarA, 2)
	putAvatar(t, s, first, 0)
	assertAvatar(t, s, first)
}

func putReplacesTheDoc(t *testing.T, s store.RoomAvatars) {
	putAvatar(t, s, RoomAvatar(avatarRoom, "acme", avatarA, 2), 0)
	cleared := RoomAvatar(avatarRoom, "acme", "", 3)
	putAvatar(t, s, cleared, 2)
	assertAvatar(t, s, cleared)
	assertInUse(t, s, avatarA, false)
	again := RoomAvatar(avatarRoom, "acme", avatarB, 5)
	putAvatar(t, s, again, 3)
	assertAvatar(t, s, again)
}

func putOnAStaleVer(t *testing.T, s store.RoomAvatars) {
	current := RoomAvatar(avatarRoom, "acme", avatarA, 2)
	putAvatar(t, s, current, 0)
	for _, prev := range []uint64{1, 0, 3} {
		if err := s.Put(t.Context(), RoomAvatar(avatarRoom, "acme", avatarB, 4), prev); !errors.Is(err, domain.ErrRetryLater) {
			t.Fatalf("Put on prev %d = %v, want ErrRetryLater", prev, err)
		}
	}
	assertAvatar(t, s, current)
}

func inUseSeesEveryRoom(t *testing.T, s store.RoomAvatars) {
	putAvatar(t, s, RoomAvatar(avatarRoom, "acme", avatarA, 2), 0)
	putAvatar(t, s, RoomAvatar(otherRoom, "acme", avatarA, 1), 0)
	putAvatar(t, s, RoomAvatar(avatarRoom, "acme", avatarB, 3), 2)
	assertInUse(t, s, avatarA, true)
	assertInUse(t, s, avatarB, true)
	putAvatar(t, s, RoomAvatar(otherRoom, "acme", "", 2), 1)
	assertInUse(t, s, avatarA, false)
	assertInUse(t, s, "acme/an/pub/20261113/never", false)
}

func inUseIgnoresOtherTenants(t *testing.T, s store.RoomAvatars) {
	putAvatar(t, s, RoomAvatar(avatarRoom, "other", foreignAvatar, 1), 0)
	assertInUse(t, s, avatarA, false)
	assertInUse(t, s, foreignAvatar, true)
}

func putWithoutAVer(t *testing.T, s store.RoomAvatars) {
	if err := s.Put(t.Context(), RoomAvatar(avatarRoom, "acme", avatarA, 0), 0); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Put(info_ver 0) = %v, want ErrInvalid", err)
	}
	if _, found, err := s.Get(t.Context(), avatarRoom); err != nil || found {
		t.Fatalf("Get after a refused put = %v, %v", found, err)
	}
}
