package roomcache

import (
	"fmt"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const PathTTL = 60 * time.Second

type ListPath uint8

const (
	PathJoin ListPath = iota + 1
	PathApp
)

type pathKey struct {
	tenant string
	user   string
}

type chosenPath struct {
	path     ListPath
	chosenAt time.Time
}

type PathCache struct {
	stats
	mu      sync.Mutex
	entries *lru.Cache[pathKey, chosenPath]
}

func NewPathCache(entries int) (*PathCache, error) {
	if entries < 1 {
		return nil, fmt.Errorf("%w: room list path cache needs at least one entry", apperr.ErrInvalidArgument)
	}
	return &PathCache{entries: lru.New[pathKey, chosenPath](entries)}, nil
}

func ChoosePath(rooms, joinMax int) ListPath {
	if rooms > joinMax {
		return PathApp
	}
	return PathJoin
}

func (c *PathCache) Get(tenant, user string) (ListPath, bool) {
	k := pathKey{tenant: tenant, user: user}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries.Get(k)
	if ok && time.Since(e.chosenAt) >= PathTTL {
		c.entries.Remove(k)
		ok = false
	}
	c.count(ok)
	if !ok {
		return 0, false
	}
	return e.path, true
}

func (c *PathCache) Put(tenant, user string, p ListPath) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries.Put(pathKey{tenant: tenant, user: user}, chosenPath{path: p, chosenAt: time.Now()})
}
