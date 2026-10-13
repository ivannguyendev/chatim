package mutate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func prefer(user string, c domain.PreferencesChange) mutate.PreferencesCmd {
	return mutate.PreferencesCmd{Tenant: tenant, User: user, Room: room, Change: c}
}

func (rg *rig) prefer(t *testing.T, user string, c domain.PreferencesChange) mutate.PreferencesResult {
	t.Helper()
	res, err := rg.m.SetRoomPreferences(t.Context(), prefer(user, c))
	if err != nil {
		t.Fatalf("SetRoomPreferences(%s, %+v): %v", user, c, err)
	}
	return res
}

func TestEachPreferenceChangesOnItsOwnAndSendsTheFullState(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMany(t, 3)
	rg.markRead(t, "bob", room, 2)
	later := rg.at().Add(time.Hour)
	steps := []struct {
		name   string
		change domain.PreferencesChange
		want   domain.RoomPreferences
	}{
		{"pin", domain.PreferencesChange{Pinned: new(true)}, domain.RoomPreferences{PinnedAt: rg.at(), Ver: 1}},
		{"mute", domain.PreferencesChange{MutedUntil: new(later)}, domain.RoomPreferences{PinnedAt: rg.at(), MutedUntil: later, Ver: 2}},
		{"archive", domain.PreferencesChange{Archived: new(true)}, domain.RoomPreferences{PinnedAt: rg.at(), MutedUntil: later, ArchivedAt: rg.at(), Ver: 3}},
		{"unmute by a past time", domain.PreferencesChange{MutedUntil: new(created)}, domain.RoomPreferences{PinnedAt: rg.at(), ArchivedAt: rg.at(), Ver: 4}},
		{"unpin", domain.PreferencesChange{Pinned: new(false)}, domain.RoomPreferences{ArchivedAt: rg.at(), Ver: 5}},
	}
	for i, s := range steps {
		res := rg.prefer(t, "bob", s.change)
		if !res.Changed || res.Member.Preferences != s.want || res.Member.Role != domain.RoleMember || res.Member.ReadSeq != 2 {
			t.Fatalf("%s = %+v, want changed to %+v with bob's role and read position", s.name, res, s.want)
		}
		_, events := rg.events.list()
		ev := events[len(events)-1]
		state := ev.GetRoomPreferencesChanged().GetState()
		if len(events) != i+2 || ev.GetId() != pbconv.PreferencesEventID(room, "bob", s.want.Ver) || ev.GetActor() != "bob" ||
			ev.GetRoomPreferencesChanged().GetUser() != "bob" || state.GetPreferenceVer() != s.want.Ver || state.GetReadSeq() != 2 ||
			(state.GetMutedUntil() != nil) != !s.want.MutedUntil.IsZero() || (state.GetPinnedAt() != nil) != !s.want.PinnedAt.IsZero() {
			t.Fatalf("%s: events = %v, want room_preferences_changed v%d with the full state last", s.name, events, s.want.Ver)
		}
	}
	if doc := rg.memberOf(t, room, "bob"); doc.Preferences != steps[len(steps)-1].want || doc.Ver != rg.memberOf(t, room, "carol").Ver {
		t.Fatalf("bob doc = %+v, want only the preferences moved", doc)
	}
}

func TestAPreferenceThatChangesNothingSendsNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.prefer(t, "bob", domain.PreferencesChange{Pinned: new(true)})
	rg.now = rg.now.Add(time.Minute)
	res := rg.prefer(t, "bob", domain.PreferencesChange{Pinned: new(true), Archived: new(false), MutedUntil: new(created)})
	if res.Changed || res.Member.Preferences.Ver != 1 || !res.Member.Preferences.PinnedAt.Equal(created.Add(time.Minute+1500*time.Microsecond).Truncate(time.Millisecond)) {
		t.Fatalf("repeat = %+v, want unchanged at preference_ver 1 with the first pin time", res)
	}
	if _, events := rg.events.list(); len(events) != 1 {
		t.Fatalf("events = %v, want only the first change", events)
	}
}

func TestPreferencesNeedAMemberAndNoPolicy(t *testing.T) {
	deny := access.PolicyFunc(func(_ context.Context, req access.Request) error { return access.ErrDenied })
	rg := newRig(t, deny)
	if res := rg.prefer(t, "bob", domain.PreferencesChange{Archived: new(true)}); !res.Changed {
		t.Fatalf("SetRoomPreferences under a deny-all policy = %+v, want changed", res)
	}
	for name, c := range map[string]mutate.PreferencesCmd{
		"outsider":     prefer("dave", domain.PreferencesChange{Pinned: new(true)}),
		"other tenant": {Tenant: "other", User: "bob", Room: room, Change: domain.PreferencesChange{Pinned: new(true)}},
		"nothing":      prefer("dave", domain.PreferencesChange{}),
	} {
		want := apperr.ErrPermissionDenied
		switch name {
		case "other tenant":
			want = apperr.ErrNotFound
		case "nothing":
			want = apperr.ErrInvalidArgument
		}
		if _, err := rg.m.SetRoomPreferences(t.Context(), c); !errors.Is(err, want) {
			t.Fatalf("%s: SetRoomPreferences = %v, want %v", name, err, want)
		}
	}
	if _, events := rg.events.list(); len(events) != 1 {
		t.Fatalf("events = %v, want only bob's archive", events)
	}
}

func TestALeftMemberCannotSetPreferences(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.LeaveRoom(t.Context(), leave("mia")); err != nil {
		t.Fatalf("leave: %v", err)
	}
	c := mutate.PreferencesCmd{Tenant: tenant, User: "mia", Room: group, Change: domain.PreferencesChange{Pinned: new(true)}}
	if _, err := rg.m.SetRoomPreferences(t.Context(), c); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("left member SetRoomPreferences = %v, want PermissionDenied", err)
	}
}
