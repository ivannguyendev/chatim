package effects

import (
	"context"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type roomTypes struct {
	rooms RoomReader
	limit int
	mu    sync.Mutex
	cache map[uint64]domain.RoomType
}

func newRoomTypes(rooms RoomReader, limit int) *roomTypes {
	return &roomTypes{rooms: rooms, limit: limit, cache: make(map[uint64]domain.RoomType)}
}

func (c *roomTypes) get(ctx context.Context, id uint64) (domain.RoomType, error) {
	c.mu.Lock()
	t, ok := c.cache[id]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	room, err := c.rooms.Get(ctx, id)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= c.limit {
		clear(c.cache)
	}
	c.cache[id] = room.Type
	return room.Type, nil
}
