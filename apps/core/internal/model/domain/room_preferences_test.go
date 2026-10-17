package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var prefNow = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func TestValidatePreferencesChangeNeedsAField(t *testing.T) {
	assertInvalid(t, domain.ValidatePreferencesChange(domain.PreferencesChange{}), "preferences")
	for _, c := range []domain.PreferencesChange{
		{Pinned: new(false)},
		{Archived: new(true)},
		{MutedUntil: new(time.Time{})},
	} {
		if err := domain.ValidatePreferencesChange(c); err != nil {
			t.Fatalf("ValidatePreferencesChange(%+v) = %v, want nil", c, err)
		}
	}
}

func TestPreferencesChangeApply(t *testing.T) {
	earlier := prefNow.Add(-time.Hour)
	later := prefNow.Add(24 * time.Hour)
	pinnedPrefs := domain.RoomPreferences{PinnedAt: earlier, Ver: 4}
	cases := []struct {
		name    string
		cur     domain.RoomPreferences
		c       domain.PreferencesChange
		want    domain.RoomPreferences
		changed bool
	}{
		{"pin", domain.RoomPreferences{Ver: 4}, domain.PreferencesChange{Pinned: new(true)}, domain.RoomPreferences{PinnedAt: prefNow, Ver: 5}, true},
		{"pin again keeps the first time", pinnedPrefs, domain.PreferencesChange{Pinned: new(true)}, pinnedPrefs, false},
		{"unpin", pinnedPrefs, domain.PreferencesChange{Pinned: new(false)}, domain.RoomPreferences{Ver: 5}, true},
		{"unpin when not pinned", domain.RoomPreferences{Ver: 4}, domain.PreferencesChange{Pinned: new(false)}, domain.RoomPreferences{Ver: 4}, false},
		{"archive", domain.RoomPreferences{}, domain.PreferencesChange{Archived: new(true)}, domain.RoomPreferences{ArchivedAt: prefNow, Ver: 1}, true},
		{"unarchive", domain.RoomPreferences{ArchivedAt: earlier, Ver: 1}, domain.PreferencesChange{Archived: new(false)}, domain.RoomPreferences{Ver: 2}, true},
		{"mute", domain.RoomPreferences{}, domain.PreferencesChange{MutedUntil: new(later)}, domain.RoomPreferences{MutedUntil: later, Ver: 1}, true},
		{"mute to the same time", domain.RoomPreferences{MutedUntil: later, Ver: 1}, domain.PreferencesChange{MutedUntil: new(later)}, domain.RoomPreferences{MutedUntil: later, Ver: 1}, false},
		{"mute in the past unmutes", domain.RoomPreferences{MutedUntil: later, Ver: 1}, domain.PreferencesChange{MutedUntil: new(earlier)}, domain.RoomPreferences{Ver: 2}, true},
		{"mute until now unmutes", domain.RoomPreferences{MutedUntil: later, Ver: 1}, domain.PreferencesChange{MutedUntil: new(prefNow)}, domain.RoomPreferences{Ver: 2}, true},
		{"past mute when not muted", domain.RoomPreferences{Ver: 1}, domain.PreferencesChange{MutedUntil: new(earlier)}, domain.RoomPreferences{Ver: 1}, false},
		{
			"three at once", domain.RoomPreferences{Ver: 7},
			domain.PreferencesChange{Pinned: new(true), Archived: new(true), MutedUntil: new(later)},
			domain.RoomPreferences{PinnedAt: prefNow, ArchivedAt: prefNow, MutedUntil: later, Ver: 8}, true,
		},
	}
	for _, c := range cases {
		got, changed := c.c.Apply(c.cur, prefNow)
		if changed != c.changed || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Apply = %+v, %v; want %+v, %v", c.name, got, changed, c.want, c.changed)
		}
	}
}

func TestPreferencesChangeApplyStoresMilliseconds(t *testing.T) {
	at := prefNow.Add(123_456_789 * time.Nanosecond).In(time.FixedZone("ICT", 7*3600))
	until := prefNow.Add(time.Hour + 987_654_321*time.Nanosecond)
	got, _ := domain.PreferencesChange{Pinned: new(true), MutedUntil: new(until)}.Apply(domain.RoomPreferences{}, at)
	want := domain.RoomPreferences{PinnedAt: prefNow.Add(123 * time.Millisecond), MutedUntil: prefNow.Add(time.Hour + 987*time.Millisecond), Ver: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply = %+v, want %+v", got, want)
	}
}
