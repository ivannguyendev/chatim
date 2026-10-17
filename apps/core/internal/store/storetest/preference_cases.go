package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func preferenceCases() []memberCase {
	return []memberCase{
		{"set preferences bumps the preference ver once per real change and no other ver", preferencesChange},
		{"set preferences needs an active member", preferencesNeedActive},
		{"preferences survive leaving and joining again", preferencesSurviveRejoin},
		{"set preferences rejects an invalid change", preferencesInvalid},
	}
}

func setPrefs(t *testing.T, s MemberRooms, c domain.PreferencesChange, at time.Time) (domain.RoomPreferences, bool) {
	t.Helper()
	got, changed, err := s.SetPreferences(t.Context(), roomA, "bob", c, at)
	if err != nil {
		t.Fatalf("SetPreferences(%+v at %v): %v", c, at, err)
	}
	return got, changed
}

func preferencesChange(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	far := secs(1000)
	steps := []struct {
		c       domain.PreferencesChange
		at      time.Time
		want    domain.RoomPreferences
		changed bool
	}{
		{domain.PreferencesChange{Pinned: new(true)}, secs(5), domain.RoomPreferences{PinnedAt: secs(5), Ver: 1}, true},
		{domain.PreferencesChange{Pinned: new(true)}, secs(6), domain.RoomPreferences{PinnedAt: secs(5), Ver: 1}, false},
		{domain.PreferencesChange{Pinned: new(true), MutedUntil: &far, Archived: new(true)}, secs(7), domain.RoomPreferences{PinnedAt: secs(5), MutedUntil: far, ArchivedAt: secs(7), Ver: 2}, true},
		{domain.PreferencesChange{MutedUntil: &far, Archived: new(true)}, secs(8), domain.RoomPreferences{PinnedAt: secs(5), MutedUntil: far, ArchivedAt: secs(7), Ver: 2}, false},
		{domain.PreferencesChange{MutedUntil: new(secs(1))}, secs(9), domain.RoomPreferences{PinnedAt: secs(5), ArchivedAt: secs(7), Ver: 3}, true},
		{domain.PreferencesChange{Pinned: new(false), Archived: new(false)}, secs(10), domain.RoomPreferences{Ver: 4}, true},
		{domain.PreferencesChange{Pinned: new(false), MutedUntil: new(secs(2))}, secs(11), domain.RoomPreferences{Ver: 4}, false},
	}
	for i, st := range steps {
		before := docOf(t, s, roomA, "bob")
		got, changed := setPrefs(t, s, st.c, st.at)
		if !samePreferences(got, st.want) || changed != st.changed {
			t.Fatalf("step %d: SetPreferences = %+v, %v; want %+v, %v", i, got, changed, st.want, st.changed)
		}
		after := docOf(t, s, roomA, "bob")
		lastChange := before.LastChangeAt
		if st.changed {
			lastChange = st.at
		}
		if !samePreferences(after.Preferences, st.want) || !after.LastChangeAt.Equal(lastChange) {
			t.Fatalf("step %d: doc = %+v, want preferences %+v and last change %v", i, after, st.want, lastChange)
		}
	}
	after := docOf(t, s, roomA, "bob")
	if after.Ver != bob.Ver || after.ReadVer != bob.ReadVer || after.UpdatedBy != bob.UpdatedBy || !after.UpdatedAt.Equal(bob.UpdatedAt) {
		t.Fatalf("doc after preference changes = %+v, want membership and read of %+v", after, bob)
	}
}

func preferencesNeedActive(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	removeMember(t, s, bob, secs(5))
	for user, room := range map[string]uint64{"bob": roomA, "carol": roomA, "alice": roomB} {
		_, _, err := s.SetPreferences(t.Context(), room, user, domain.PreferencesChange{Pinned: new(true)}, secs(6))
		assertErrorIs(t, "SetPreferences("+user+")", err, domain.ErrNotMember)
	}
	if got := docOf(t, s, roomA, "bob"); got.Preferences != (domain.RoomPreferences{}) {
		t.Fatalf("tombstone preferences = %+v, want none", got.Preferences)
	}
}

func preferencesSurviveRejoin(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	kept, _ := setPrefs(t, s, domain.PreferencesChange{Pinned: new(true), Archived: new(true)}, secs(3))
	removeMember(t, s, docOf(t, s, roomA, "bob"), secs(4))
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-back", By: "alice", At: secs(10)}
	if _, err := s.AddMembers(t.Context(), j, []string{"bob"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	back := docOf(t, s, roomA, "bob")
	if !back.Active() || back.Ver != bob.Ver+2 || !samePreferences(back.Preferences, kept) {
		t.Fatalf("rejoined doc = %+v, want active at ver %d with preferences %+v", back, bob.Ver+2, kept)
	}
	j.RequestID = "req-carol"
	res, err := s.AddMembers(t.Context(), j, []string{"carol"})
	if err != nil || res.Members[0].Preferences != (domain.RoomPreferences{}) {
		t.Fatalf("AddMembers(carol) = %+v, %v; want no preferences at ver 0", res, err)
	}
}

func preferencesInvalid(t *testing.T, s MemberRooms) {
	crew(t, s)
	pin := domain.PreferencesChange{Pinned: new(true)}
	for name, call := range map[string]func() error{
		"empty": func() error {
			_, _, err := s.SetPreferences(t.Context(), roomA, "bob", domain.PreferencesChange{}, secs(5))
			return err
		},
		"zero time": func() error { _, _, err := s.SetPreferences(t.Context(), roomA, "bob", pin, time.Time{}); return err },
		"bad user":  func() error { _, _, err := s.SetPreferences(t.Context(), roomA, "b b", pin, secs(5)); return err },
		"room 0":    func() error { _, _, err := s.SetPreferences(t.Context(), 0, "bob", pin, secs(5)); return err },
	} {
		assertErrorIs(t, "SetPreferences("+name+")", call(), apperr.ErrInvalidArgument)
	}
	if got := docOf(t, s, roomA, "bob"); got.Preferences != (domain.RoomPreferences{}) {
		t.Fatalf("preferences after rejected changes = %+v, want none", got.Preferences)
	}
}
