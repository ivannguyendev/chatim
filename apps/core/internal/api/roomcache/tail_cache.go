package roomcache

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const TailTTL = 30 * time.Second

type TailState uint8

const (
	TailFresh TailState = iota + 1
	TailExtend
	TailReload
)

type cachedTail struct {
	tail     Tail
	loadedAt time.Time
}

type TailCache struct {
	stats
	mu      sync.Mutex
	entries *sizedLRU[uint64, cachedTail]
	flight  singleflight.Group
}

func NewTailCache(maxBytes int) (*TailCache, error) {
	if maxBytes < fullTailBytes {
		return nil, fmt.Errorf("%w: room tail cache needs at least %d bytes", apperr.ErrInvalidArgument, fullTailBytes)
	}
	return &TailCache{entries: newSizedLRU[uint64, cachedTail](maxBytes)}, nil
}

func (c *TailCache) Lookup(a store.RoomActivity) (Tail, TailState) {
	t, state := c.check(a)
	c.count(state == TailFresh)
	return cloneTail(t), state
}

func (c *TailCache) Merge(room uint64, newer Tail) (Tail, bool) {
	t, ok := c.merge(room, newer)
	return cloneTail(t), ok
}

func (c *TailCache) Replace(room uint64, t Tail) Tail {
	return cloneTail(c.replace(room, t))
}

func (c *TailCache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.bytes()
}

func (c *TailCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.entries()
}

func (c *TailCache) live(room uint64) (cachedTail, bool) {
	e, ok := c.entries.get(room)
	if ok && time.Since(e.loadedAt) >= TailTTL {
		c.entries.remove(room)
		return cachedTail{}, false
	}
	return e, ok
}

func (c *TailCache) check(a store.RoomActivity) (Tail, TailState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.live(a.Room)
	switch {
	case !ok || !e.tail.LastEditAt.Equal(a.LastEditAt):
		return Tail{}, TailReload
	case e.tail.LastSeq < a.LastSeq:
		return e.tail, TailExtend
	default:
		return e.tail, TailFresh
	}
}

func (c *TailCache) merge(room uint64, newer Tail) (Tail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	base, ok := c.live(room)
	if !ok || !base.tail.LastEditAt.Equal(newer.LastEditAt) {
		return Tail{}, false
	}
	merged := mergeTails(base.tail, newer)
	if merged.LastSeq > base.tail.LastSeq {
		c.entries.put(room, cachedTail{tail: merged, loadedAt: base.loadedAt}, tailBytes(merged))
	}
	return merged, true
}

func (c *TailCache) replace(room uint64, t Tail) Tail {
	t = settleTail(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.live(room); ok && newerTail(old.tail, t) {
		return t
	}
	c.entries.put(room, cachedTail{tail: t, loadedAt: time.Now()}, tailBytes(t))
	return t
}

func cloneTail(t Tail) Tail {
	t.Window, t.Previews = slices.Clone(t.Window), slices.Clone(t.Previews)
	return t
}
