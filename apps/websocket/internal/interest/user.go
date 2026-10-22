package interest

import (
	"context"
	"sync"
	"time"
)

type change struct {
	seq   uint64
	room  uint64
	ver   uint32
	added bool
}

type user struct {
	key         userKey
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	ready       bool
	gone        bool
	loading     bool
	suspended   bool
	suspendedAt time.Time
	gen         uint64
	mark        uint64
	loadMark    uint64
	conns       map[*Conn]struct{}
	rooms       map[uint64]uint32
	left        map[uint64]uint32
	held        []change
}

func newUser(ctx context.Context, k userKey) *user {
	u := &user{key: k, gen: 1, conns: map[*Conn]struct{}{}, rooms: map[uint64]uint32{}, left: map[uint64]uint32{}}
	u.ctx, u.cancel = context.WithCancel(ctx)
	return u
}

func (u *user) applyAdded(room uint64, ver uint32) (applied, wasMember bool) {
	cur, wasMember := u.rooms[room]
	if wasMember && ver <= cur {
		return false, true
	}
	if gone, ok := u.left[room]; ok && ver <= gone {
		return false, false
	}
	u.rooms[room] = ver
	delete(u.left, room)
	return true, wasMember
}

func (u *user) applyRemoved(room uint64, ver uint32) (applied, wasMember bool) {
	if cur, ok := u.rooms[room]; ok {
		if ver <= cur {
			return false, false
		}
		delete(u.rooms, room)
		u.left[room] = ver
		return true, true
	}
	if gone, ok := u.left[room]; ok && ver <= gone {
		return false, false
	}
	u.left[room] = ver
	return true, false
}

func (u *user) apply(ch change) {
	if ch.added {
		u.applyAdded(ch.room, ch.ver)
		return
	}
	u.applyRemoved(ch.room, ch.ver)
}

func (u *user) live() bool { return u.ready && !u.suspended && !u.gone }

func (t *Table) suspend(u *user, now time.Time) {
	if u.gone {
		return
	}
	u.gen++
	u.loading = false
	u.held = nil
	if !u.suspended {
		u.suspended = true
		u.suspendedAt = now
		t.stats.suspended.Add(1)
	}
	for c := range u.conns {
		c.suspend()
	}
}

func (t *Table) resume(u *user) bool {
	if !u.suspended {
		return false
	}
	u.suspended = false
	t.stats.suspended.Add(-1)
	return true
}

func (u *user) deliver(frame []byte, catchingUp, resumes bool) uint64 {
	var n uint64
	for c := range u.conns {
		if c.deliver(frame, catchingUp, resumes) {
			n++
		}
	}
	return n
}

func (t *Table) Register(tenant, userID string, sink Sink) *Conn {
	c, waiting := t.attach(userKey{tenant: tenant, user: userID}, sink)
	if waiting {
		t.prioritize(c.user)
	}
	return c
}

func (t *Table) attach(k userKey, sink Sink) (*Conn, bool) {
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	if t.closed {
		u := newUser(t.life, k)
		u.gone = true
		c := newConn(t, u, sink)
		c.gone = true
		sink.Fail()
		return c, false
	}
	s := t.userShard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, found := s.users[k]
	if !found {
		u = newUser(t.life, k)
		s.users[k] = u
		t.stats.users.Add(1)
	}
	c := newConn(t, u, sink)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.conns[c] = struct{}{}
	switch {
	case !found:
		t.loads.Go(func() { t.initialLoad(u) })
	case u.suspended:
		return c, true
	case u.ready:
		c.readied = true
		sink.Send(readyFrame)
	}
	return c, false
}

func (c *Conn) Unregister() {
	t, u := c.table, c.user
	s := t.userShard(u.key)
	s.mu.Lock()
	defer s.mu.Unlock()
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.gone {
		return
	}
	c.release()
	delete(u.conns, c)
	if len(u.conns) > 0 || u.gone {
		return
	}
	t.drop(s, u)
	u.rooms, u.left = nil, nil
}

func (t *Table) drop(s *userShard, u *user) {
	u.gone = true
	t.resume(u)
	if s.users[u.key] == u {
		delete(s.users, u.key)
		t.stats.users.Add(-1)
	}
	u.cancel()
	if u.ready {
		for room := range u.rooms {
			t.unindex(u, room)
		}
	}
	u.held = nil
}
