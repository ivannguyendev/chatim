package groups

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/recipients"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const (
	TTL             = 60 * time.Second
	CachedTenants   = 1024
	recentForgotten = 4096
)

var _ recipients.Catalog = (*Catalog)(nil)

type cached struct {
	active   []domain.Group
	loadedAt time.Time
}

type Catalog struct {
	groups   store.Groups
	flight   singleflight.Group
	mu       sync.Mutex
	entries  *lru.Cache[string, cached]
	forgets  *lru.Cache[string, uint64]
	forgetAt uint64
}

func New(groups store.Groups) *Catalog {
	return &Catalog{
		groups:  groups,
		entries: lru.New[string, cached](CachedTenants),
		forgets: lru.New[string, uint64](recentForgotten),
	}
}

func (c *Catalog) Active(ctx context.Context, tenant string) ([]domain.Group, error) {
	if hit, ok := c.get(tenant); ok {
		return slices.Clone(hit), nil
	}
	v, err, _ := c.flight.Do(tenant, func() (any, error) { return c.load(ctx, tenant) })
	if err != nil {
		return nil, err
	}
	return slices.Clone(v.([]domain.Group)), nil
}

func (c *Catalog) Taggable(ctx context.Context, tenant string) (map[string]bool, error) {
	active, err := c.Active(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(active))
	for _, g := range active {
		out[g.ID] = true
	}
	return out, nil
}

func (c *Catalog) Forget(tenant string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgetAt++
	c.entries.Remove(tenant)
	c.forgets.Put(tenant, c.forgetAt)
}

func (c *Catalog) get(tenant string) ([]domain.Group, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries.Get(tenant)
	if !ok {
		return nil, false
	}
	if time.Since(e.loadedAt) >= TTL {
		c.entries.Remove(tenant)
		return nil, false
	}
	return e.active, true
}

func (c *Catalog) load(ctx context.Context, tenant string) ([]domain.Group, error) {
	mark := c.mark()
	all, err := c.groups.All(ctx, tenant)
	if err != nil {
		return nil, err
	}
	active := slices.DeleteFunc(slices.Clone(all), func(g domain.Group) bool { return !g.Active })
	slices.SortFunc(active, func(a, b domain.Group) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	c.put(mark, tenant, active)
	return active, nil
}

func (c *Catalog) mark() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forgetAt
}

func (c *Catalog) put(mark uint64, tenant string, active []domain.Group) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.forgetAt-mark >= recentForgotten {
		return
	}
	if at, forgot := c.forgets.Peek(tenant); forgot && at > mark {
		return
	}
	c.entries.Put(tenant, cached{active: active, loadedAt: time.Now()})
}
