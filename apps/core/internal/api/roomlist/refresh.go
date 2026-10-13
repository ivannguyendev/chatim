package roomlist

import (
	"context"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func (l *Lister) Get(ctx context.Context, tenant, user string, rooms []uint64) ([]domain.RoomItem, []uint64, error) {
	if len(rooms) > MaxRooms {
		return nil, nil, errRoomIDs
	}
	slots := make([]slot, 0, len(rooms))
	seen := make(map[uint64]struct{}, len(rooms))
	for _, id := range rooms {
		if id == 0 || id > math.MaxInt64 {
			return nil, nil, errRoomIDs
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			slots = append(slots, slot{room: id})
		}
	}
	return l.build(ctx, who{tenant: tenant, user: user}, slots)
}
