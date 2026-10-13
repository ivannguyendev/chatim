package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type RoomPreferences interface {
	SetPreferences(ctx context.Context, room uint64, user string, c domain.PreferencesChange, at time.Time) (domain.RoomPreferences, bool, error)
}

func ValidatePreferences(room uint64, user string, c domain.PreferencesChange, at time.Time) error {
	switch {
	case room == 0:
		return invalid("room id")
	case at.IsZero():
		return invalid("time")
	}
	if err := domain.ValidUser(user); err != nil {
		return err
	}
	return domain.ValidatePreferencesChange(c)
}
