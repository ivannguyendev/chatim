package memstore

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.RoomUpdater = (*Rooms)(nil)

func (s *Rooms) UpdateRoomInfo(ctx context.Context, room, base uint64, c store.RoomInfoChange) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateRoomInfoChange(room, c); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, base+1); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok || r.InfoVer != base {
		return false, nil
	}
	if c.Update.Name != nil {
		r.Name = *c.Update.Name
	}
	if c.Update.Avatar != nil {
		r.Avatar = *c.Update.Avatar
	}
	r.InfoVer, r.UpdatedAt, r.UpdatedBy = base+1, toMillis(c.At), c.By
	s.rooms[room] = r
	s.roomFlags.raise(room, store.BitRoomInfo, time.Now())
	s.logRoomInfoLocked(room, r.InfoVer)
	return true, nil
}
