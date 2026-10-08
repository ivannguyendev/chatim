package actor

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const memberCacheTTL = 10 * time.Second

type cachedMember struct {
	member domain.Member
	readAt time.Time
}

type memberCache struct {
	gen     atomic.Uint64
	seen    uint64
	entries *lru.Cache[string, cachedMember]
}

func newMemberCache() *memberCache {
	return &memberCache{entries: lru.New[string, cachedMember](memberCacheSize)}
}

func (c *memberCache) forget() { c.gen.Add(1) }

func (c *memberCache) sync() {
	if gen := c.gen.Load(); gen != c.seen {
		c.entries = lru.New[string, cachedMember](memberCacheSize)
		c.seen = gen
	}
}

func (c *memberCache) fresh(user string, now time.Time) (domain.Member, bool) {
	e, ok := c.entries.Get(user)
	if !ok || now.Sub(e.readAt) >= memberCacheTTL {
		return domain.Member{}, false
	}
	return e.member, true
}

func (r *Router) ForgetMembers(room uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a := r.actors[room]; a != nil {
		a.members.forget()
	}
}

func (a *actor) member(ctx context.Context, user string) (domain.Member, error) {
	a.members.sync()
	now := time.Now()
	if m, ok := a.members.fresh(user, now); ok {
		return m, nil
	}
	m, err := a.r.rooms.Member(ctx, a.id, user)
	switch {
	case err == nil:
		a.members.entries.Put(user, cachedMember{member: m, readAt: now})
		return m, nil
	case errors.Is(err, domain.ErrNotMember):
		a.members.entries.Remove(user)
		return domain.Member{}, domain.ErrNotMember
	default:
		a.r.log.WarnContext(ctx, "membership check failed", "room", a.id, "err", err)
		return domain.Member{}, errUnavailable
	}
}
