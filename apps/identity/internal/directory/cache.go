package directory

import (
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
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

type Cache struct {
	mu       sync.RWMutex
	users    map[store.UserKey]Entry
	position store.Position
	token    store.StreamToken
	ready    atomic.Bool
}

func NewCache() *Cache {
	return &Cache{users: map[store.UserKey]Entry{}}
}

func (c *Cache) Replace(users map[store.UserKey]Entry, token store.StreamToken) {
	c.mu.Lock()
	c.users, c.token = users, token
	c.mu.Unlock()
	c.ready.Store(true)
}

func (c *Cache) ApplyEntry(e store.DirectoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	putEntry(c.users, e)
}

func (c *Cache) Token() store.StreamToken {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

func (c *Cache) SetToken(t store.StreamToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = t
}

func putEntry(users map[store.UserKey]Entry, e store.DirectoryEntry) {
	putUser(users, store.UserKey{TenantID: e.TenantID, ID: e.ID}, Entry{DisplayName: e.DisplayName, AvatarURL: e.AvatarURL, State: e.State, Ver: e.Ver})
}

func (c *Cache) ApplyEntries(entries []store.DirectoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		putEntry(c.users, e)
	}
}

func (c *Cache) Apply(changes []store.Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range changes {
		if ch.Kind != store.ChangeUser || ch.User == nil {
			continue
		}
		putUser(c.users, store.UserKey{TenantID: ch.TenantID, ID: ch.ID}, Entry{DisplayName: ch.User.DisplayName, AvatarURL: ch.User.AvatarURL, State: ch.User.State, Ver: ch.Ver})
	}
}

func putUser(users map[store.UserKey]Entry, key store.UserKey, e Entry) {
	if e.State != store.StateActive && e.State != store.StateDisabled {
		return
	}
	if old, ok := users[key]; ok && e.Ver <= old.Ver {
		return
	}
	users[key] = e
}

func (c *Cache) Lookup(tenant string, ids []string) []User {
	c.mu.RLock()
	defer c.mu.RUnlock()
	found := make([]User, 0, len(ids))
	for _, id := range ids {
		if e, ok := c.users[store.UserKey{TenantID: tenant, ID: id}]; ok {
			found = append(found, User{ID: id, Entry: e})
		}
	}
	return found
}

func (c *Cache) LookupOne(tenant, id string) (User, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.users[store.UserKey{TenantID: tenant, ID: id}]
	if !ok {
		return User{}, false
	}
	return User{ID: id, Entry: e}, true
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
