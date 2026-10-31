package directory

import (
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

type Entry struct {
	DisplayName string
	AvatarURL   string
	State       store.State
	Ver         int64
}

type User struct {
	ID string
	Entry
}

type userKey struct {
	tenant string
	id     string
}

type Cache struct {
	mu       sync.RWMutex
	users    map[userKey]Entry
	position store.Position
	ready    atomic.Bool
}

func NewCache() *Cache {
	return &Cache{users: map[userKey]Entry{}}
}

func (c *Cache) ApplyEntries(entries []store.DirectoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		c.put(userKey{e.TenantID, e.ID}, Entry{DisplayName: e.DisplayName, AvatarURL: e.AvatarURL, State: e.State, Ver: e.Ver})
	}
}

func (c *Cache) Apply(changes []store.Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range changes {
		if ch.Kind != store.ChangeUser || ch.User == nil {
			continue
		}
		c.put(userKey{ch.TenantID, ch.ID}, Entry{DisplayName: ch.User.DisplayName, AvatarURL: ch.User.AvatarURL, State: ch.User.State, Ver: ch.Ver})
	}
}

func (c *Cache) put(key userKey, e Entry) {
	if e.State != store.StateActive && e.State != store.StateDisabled {
		return
	}
	if old, ok := c.users[key]; ok && e.Ver <= old.Ver {
		return
	}
	c.users[key] = e
}

func (c *Cache) Lookup(tenant string, ids []string) []User {
	c.mu.RLock()
	defer c.mu.RUnlock()
	found := make([]User, 0, len(ids))
	for _, id := range ids {
		if e, ok := c.users[userKey{tenant, id}]; ok {
			found = append(found, User{ID: id, Entry: e})
		}
	}
	return found
}

func (c *Cache) Position() store.Position {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.position
}

func (c *Cache) SetPosition(p store.Position) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.position = p
}

func (c *Cache) Ready() bool { return c.ready.Load() }

func (c *Cache) markReady() { c.ready.Store(true) }

func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.users)
}
