package domain

import "time"

type RoomPreferences struct {
	PinnedAt   time.Time
	MutedUntil time.Time
	ArchivedAt time.Time
	Ver        uint64
}

type PreferencesChange struct {
	Pinned     *bool
	MutedUntil *time.Time
	Archived   *bool
}

func ValidatePreferencesChange(c PreferencesChange) error {
	if c.Pinned == nil && c.MutedUntil == nil && c.Archived == nil {
		return invalid("preferences")
	}
	return nil
}

func (c PreferencesChange) Apply(cur RoomPreferences, at time.Time) (RoomPreferences, bool) {
	at = at.UTC().Truncate(time.Millisecond)
	next := cur
	if c.Pinned != nil {
		next.PinnedAt = toggledAt(cur.PinnedAt, *c.Pinned, at)
	}
	if c.Archived != nil {
		next.ArchivedAt = toggledAt(cur.ArchivedAt, *c.Archived, at)
	}
	if c.MutedUntil != nil {
		next.MutedUntil = mutedUntil(*c.MutedUntil, at)
	}
	if next.PinnedAt.Equal(cur.PinnedAt) && next.ArchivedAt.Equal(cur.ArchivedAt) && next.MutedUntil.Equal(cur.MutedUntil) {
		return cur, false
	}
	next.Ver = cur.Ver + 1
	return next, true
}

func toggledAt(cur time.Time, on bool, at time.Time) time.Time {
	switch {
	case !on:
		return time.Time{}
	case cur.IsZero():
		return at
	default:
		return cur
	}
}

func mutedUntil(until, at time.Time) time.Time {
	until = until.UTC().Truncate(time.Millisecond)
	if !until.After(at) {
		return time.Time{}
	}
	return until
}
