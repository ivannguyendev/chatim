package presence

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"
)

type cacheShard struct {
	mu     sync.Mutex
	readAt map[user]time.Time
}

type onlineCache struct {
	fresh  time.Duration
	seed   maphash.Seed
	shards [shardCount]cacheShard
	size   atomic.Int64
}

func newOnlineCache(fresh time.Duration) *onlineCache {
	c := &onlineCache{fresh: fresh, seed: maphash.MakeSeed()}
	for i := range c.shards {
		c.shards[i].readAt = map[user]time.Time{}
	}
	return c
}

func (c *onlineCache) online(u user, now time.Time) bool {
	sh := c.shardOf(u)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	readAt, ok := sh.readAt[u]
	return ok && now.Sub(readAt) < c.fresh
}

func (c *onlineCache) put(u user, readAt time.Time) {
	sh := c.shardOf(u)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, ok := sh.readAt[u]; !ok {
		c.size.Add(1)
	}
	sh.readAt[u] = readAt
}

func (c *onlineCache) forget(u user) {
	sh := c.shardOf(u)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, ok := sh.readAt[u]; ok {
		delete(sh.readAt, u)
		c.size.Add(-1)
	}
}

func (c *onlineCache) prune(now time.Time) {
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		for u, readAt := range sh.readAt {
			if now.Sub(readAt) >= c.fresh {
				delete(sh.readAt, u)
				c.size.Add(-1)
			}
		}
		sh.mu.Unlock()
	}
}

func (c *onlineCache) shardOf(u user) *cacheShard {
	return &c.shards[maphash.Comparable(c.seed, u)%shardCount]
}
