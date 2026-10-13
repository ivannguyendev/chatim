package roomcache

import (
	"fmt"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type TailState uint8

const (
	TailFresh TailState = iota + 1
	TailExtend
	TailReload
)

type TailCache struct {
	stats
	mu      sync.Mutex
	entries *sizedLRU[uint64, Tail]
	flight  singleflight.Group
}

func NewTailCache(maxBytes int) (*TailCache, error) {
	if maxBytes < tailEntryBytes {
		return nil, fmt.Errorf("%w: room tail cache needs at least %d bytes", apperr.ErrInvalidArgument, tailEntryBytes)
	}
	return &TailCache{entries: newSizedLRU[uint64, Tail](maxBytes)}, nil
}

func (c *TailCache) Lookup(a store.RoomActivity) (Tail, TailState) {
	t, state := c.check(a)
	c.count(state == TailFresh)
	return t, state
}

func (c *TailCache) Merge(room uint64, newer Tail) (Tail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	base, ok := c.entries.get(room)
	if !ok || !base.LastEditAt.Equal(newer.LastEditAt) {
		return Tail{}, false
	}
	merged := mergeTails(base, newer)
	if merged.LastSeq > base.LastSeq {
		c.entries.put(room, merged, tailBytes(merged))
	}
	return merged, true
}

func (c *TailCache) Replace(room uint64, t Tail) Tail {
	t = settleTail(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries.get(room); ok && newerTail(old, t) {
		return t
	}
	c.entries.put(room, t, tailBytes(t))
	return t
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

func (c *TailCache) check(a store.RoomActivity) (Tail, TailState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.entries.get(a.Room)
	switch {
	case !ok || !t.LastEditAt.Equal(a.LastEditAt):
		return Tail{}, TailReload
	case t.LastSeq < a.LastSeq:
		return t, TailExtend
	default:
		return t, TailFresh
	}
}
