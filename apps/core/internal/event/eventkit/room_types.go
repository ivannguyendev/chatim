package eventkit

import (
	"context"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type RoomTypes struct {
	rooms RoomReader
	limit int
	mu    sync.Mutex
	cache map[uint64]domain.Room
}

func NewRoomTypes(rooms RoomReader, limit int) *RoomTypes {
	return &RoomTypes{rooms: rooms, limit: limit, cache: make(map[uint64]domain.Room)}
}

func (c *RoomTypes) Type(ctx context.Context, id uint64) (domain.RoomType, error) {
	r, err := c.Identity(ctx, id)
	return r.Type, err
}

func (c *RoomTypes) Identity(ctx context.Context, id uint64) (domain.Room, error) {
	c.mu.Lock()
	r, ok := c.cache[id]
	c.mu.Unlock()
	if ok {
		return r, nil
	}
	room, err := c.rooms.Get(ctx, id)
	if err != nil {
		return domain.Room{}, err
	}
	r = domain.Room{ID: room.ID, Tenant: room.Tenant, Type: room.Type}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= c.limit {
		clear(c.cache)
	}
	c.cache[id] = r
	return r, nil
}
