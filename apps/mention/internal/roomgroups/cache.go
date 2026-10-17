package roomgroups

import (
	"fmt"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const recentForgetRooms = 4096

type Entry struct {
	Groups  map[string]struct{}
	BuiltAt time.Time
	Small   bool
}

type Cache struct {
	mu       sync.Mutex
	entries  *lru.Cache[uint64, Entry]
	forgets  *lru.Cache[uint64, uint64]
	forgetAt uint64
}

func NewCache(entries int) (*Cache, error) {
	if entries < 1 {
		return nil, fmt.Errorf("%w: room groups cache needs at least one entry", apperr.ErrInvalidArgument)
	}
	return &Cache{
		entries: lru.New[uint64, Entry](entries),
		forgets: lru.New[uint64, uint64](recentForgetRooms),
	}, nil
}

func (c *Cache) Mark() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forgetAt
}

func (c *Cache) Get(room uint64) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.Get(room)
}

func (c *Cache) Put(mark, room uint64, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.Small && c.forgotSince(mark, room) {
		return
	}
	c.entries.Put(room, e)
}

func (c *Cache) Forget(room uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgetAt++
	c.forgets.Put(room, c.forgetAt)
	if e, ok := c.entries.Peek(room); ok && e.Small {
		c.entries.Remove(room)
	}
}

func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.Len()
}

func (c *Cache) forgotSince(mark, room uint64) bool {
	if c.forgetAt-mark >= recentForgetRooms {
		return true
	}
	at, forgot := c.forgets.Peek(room)
	return forgot && at > mark
}
