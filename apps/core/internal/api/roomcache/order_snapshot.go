package roomcache

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	SnapshotTTL       = 30 * time.Second
	snapshotBaseBytes = 96
	snapshotKeyBytes  = 64
)

type SnapshotKey struct {
	Tenant string
	User   string
	Folder domain.RoomFolder
}

type orderSnapshot struct {
	keys    []domain.RoomSortKey
	takenAt time.Time
}

type SnapshotCache struct {
	stats
	mu      sync.Mutex
	entries *sizedLRU[SnapshotKey, orderSnapshot]
}

func NewSnapshotCache(maxBytes int) (*SnapshotCache, error) {
	if maxBytes < snapshotBaseBytes {
		return nil, fmt.Errorf("%w: room list snapshot cache needs at least %d bytes", apperr.ErrInvalidArgument, snapshotBaseBytes)
	}
	return &SnapshotCache{entries: newSizedLRU[SnapshotKey, orderSnapshot](maxBytes)}, nil
}

func (c *SnapshotCache) Store(k SnapshotKey, keys []domain.RoomSortKey) {
	sorted := slices.Clone(keys)
	slices.SortFunc(sorted, domain.CompareRoomSortKeys)
	size := snapshotBaseBytes + len(k.Tenant) + len(k.User) + snapshotKeyBytes*len(sorted)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries.put(k, orderSnapshot{keys: sorted, takenAt: time.Now()}, size)
}

func (c *SnapshotCache) Page(k SnapshotKey, after *domain.RoomSortKey, limit int) ([]domain.RoomSortKey, bool, bool) {
	keys, ok := c.current(k)
	c.count(ok)
	if !ok {
		return nil, false, false
	}
	start := 0
	if after != nil {
		start, _ = slices.BinarySearchFunc(keys, *after, domain.CompareRoomSortKeys)
		if start < len(keys) && domain.CompareRoomSortKeys(keys[start], *after) == 0 {
			start++
		}
	}
	end := min(len(keys), start+max(limit, 0))
	return slices.Clone(keys[start:end]), end < len(keys), true
}

func (c *SnapshotCache) Forget(k SnapshotKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries.remove(k)
}

func (c *SnapshotCache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.bytes()
}

func (c *SnapshotCache) current(k SnapshotKey) ([]domain.RoomSortKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries.get(k)
	if !ok {
		return nil, false
	}
	if time.Since(s.takenAt) >= SnapshotTTL {
		c.entries.remove(k)
		return nil, false
	}
	return s.keys, true
}
