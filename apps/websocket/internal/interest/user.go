package interest

import (
	"context"
	"sync"
)

type change struct {
	seq   uint64
	room  uint64
	ver   uint32
	added bool
}

type user struct {
	key      userKey
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	ready    bool
	gone     bool
	loading  bool
	gen      uint64
	mark     uint64
	loadMark uint64
	conns    map[*Conn]struct{}
	rooms    map[uint64]uint32
	left     map[uint64]uint32
	held     []change
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
	k := userKey{tenant: tenant, user: userID}
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	if t.closed {
		u := newUser(t.life, k)
		u.gone = true
		c := newConn(t, u, sink)
		c.gone = true
		sink.Fail()
		return c
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
	case u.ready:
		sink.Send(readyFrame)
	}
	return c
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
	u.gone = true
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
	u.rooms, u.left, u.held = nil, nil, nil
}
