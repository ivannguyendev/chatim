package roomcache

import (
	"fmt"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const (
	InfoTTL           = 60 * time.Second
	recentForgetRooms = 4096
)

type cachedInfo struct {
	room     domain.Room
	storedAt time.Time
}

type InfoCache struct {
	stats
	mu       sync.Mutex
	entries  *lru.Cache[uint64, cachedInfo]
	forgets  *lru.Cache[uint64, uint64]
	forgetAt uint64
}

func NewInfoCache(entries int) (*InfoCache, error) {
	if entries < 1 {
		return nil, fmt.Errorf("%w: room info cache needs at least one entry", apperr.ErrInvalidArgument)
	}
	return &InfoCache{
		entries: lru.New[uint64, cachedInfo](entries),
		forgets: lru.New[uint64, uint64](recentForgetRooms),
	}, nil
}

func (c *InfoCache) Mark() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forgetAt
}

func (c *InfoCache) Get(rooms []uint64) (map[uint64]domain.Room, []uint64) {
	now := time.Now()
	hits := make(map[uint64]domain.Room, len(rooms))
	seen := make(map[uint64]struct{}, len(rooms))
	var misses []uint64
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range rooms {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		e, ok := c.entries.Get(id)
		if ok && now.Sub(e.storedAt) < InfoTTL {
			hits[id] = e.room
			c.count(true)
			continue
		}
		if ok {
			c.entries.Remove(id)
		}
		misses = append(misses, id)
		c.count(false)
	}
	return hits, misses
}

func (c *InfoCache) Put(mark uint64, rooms ...domain.Room) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.forgetAt-mark >= recentForgetRooms {
		return
	}
	for _, r := range rooms {
		if at, forgot := c.forgets.Peek(r.ID); forgot && at > mark {
			continue
		}
		c.entries.Put(r.ID, cachedInfo{room: r, storedAt: now})
	}
}

func (c *InfoCache) Forget(room uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgetAt++
	c.entries.Remove(room)
	c.forgets.Put(room, c.forgetAt)
}

func (c *InfoCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.Len()
}
