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
	key    userKey
	mu     sync.Mutex
	ready  bool
	gone   bool
	mark   uint64
	cancel context.CancelFunc
	conns  map[*Conn]struct{}
	rooms  map[uint64]uint32
	left   map[uint64]uint32
	held   []change
}

func newUser(k userKey) *user {
	return &user{key: k, conns: map[*Conn]struct{}{}, rooms: map[uint64]uint32{}, left: map[uint64]uint32{}}
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

func (u *user) send(frame []byte) uint64 {
	var n uint64
	for c := range u.conns {
		c.sink.Send(frame)
		n++
	}
	return n
}

func (t *Table) Register(tenant, userID string, sink Sink) *Conn {
	k := userKey{tenant: tenant, user: userID}
	s := t.userShard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, found := s.users[k]
	if !found {
		u = newUser(k)
		s.users[k] = u
		t.stats.users.Add(1)
	}
	c := newConn(t, u, sink)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.conns[c] = struct{}{}
	switch {
	case !found:
		t.startLoad(u)
	case u.ready:
		sink.Send(readyFrame)
	}
	return c
}

func (t *Table) startLoad(u *user) {
	ctx, cancel := context.WithCancel(t.life)
	u.cancel = cancel
	if t.closed.Load() {
		cancel()
	}
	t.loads.Go(func() { t.load(ctx, u) })
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
