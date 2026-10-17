package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type RoomInfoChange struct {
	Update domain.RoomUpdate
	By     string
	At     time.Time
}

type RoomUpdater interface {
	UpdateRoomInfo(ctx context.Context, room, base uint64, c RoomInfoChange) (bool, error)
}

func ValidateRoomInfoChange(room uint64, c RoomInfoChange) error {
	switch {
	case room == 0:
		return invalid("room id")
	case c.At.IsZero():
		return invalid("time")
	}
	if err := domain.ValidUser(c.By); err != nil {
		return err
	}
	return domain.ValidateRoomUpdate(c.Update)
}
