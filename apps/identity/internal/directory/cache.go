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
	subjects map[store.SubjectKey]string
	token    store.StreamToken
	ready    atomic.Bool
}

func NewCache() *Cache {
	return &Cache{users: map[store.UserKey]Entry{}, subjects: map[store.SubjectKey]string{}}
}

func (c *Cache) Replace(users map[store.UserKey]Entry, subjects map[store.SubjectKey]string, token store.StreamToken) {
	c.mu.Lock()
	c.users, c.subjects, c.token = users, subjects, token
	c.mu.Unlock()
	c.ready.Store(true)
}

func (c *Cache) ApplyEntry(e store.DirectoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	putEntry(c.users, c.subjects, e)
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

func putEntry(users map[store.UserKey]Entry, subjects map[store.SubjectKey]string, e store.DirectoryEntry) {
	putUser(users, store.UserKey{TenantID: e.TenantID, ID: e.ID}, Entry{DisplayName: e.DisplayName, AvatarURL: e.AvatarURL, State: e.State, Ver: e.Ver})
	if e.Realm != "" && e.Sub != "" {
		subjects[store.SubjectKey{TenantID: e.TenantID, Realm: e.Realm, Sub: e.Sub}] = e.ID
	}
}

func (c *Cache) ApplyEntries(entries []store.DirectoryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		putEntry(c.users, c.subjects, e)
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

func (c *Cache) LookupSubject(tenant, realm, sub string) (User, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.subjects[store.SubjectKey{TenantID: tenant, Realm: realm, Sub: sub}]
	if !ok {
		return User{}, false
	}
	e, ok := c.users[store.UserKey{TenantID: tenant, ID: id}]
	if !ok {
		return User{}, false
	}
	return User{ID: id, Entry: e}, true
}

func (c *Cache) Ready() bool { return c.ready.Load() }

func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.users)
}
