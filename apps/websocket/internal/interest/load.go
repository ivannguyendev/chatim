package interest

import (
	"context"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (t *Table) acquire(ctx context.Context) bool {
	select {
	case t.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (t *Table) release() { <-t.slots }

func (t *Table) initialLoad(u *user) {
	if !t.acquire(u.ctx) {
		return
	}
	defer t.release()
	t.fetch(u, 1, chatimv1.ResyncReason_RESYNC_REASON_UNSPECIFIED)
}

func (t *Table) fetch(u *user, gen uint64, reason chatimv1.ResyncReason) {
	u.mu.Lock()
	if u.gone || u.gen != gen {
		u.mu.Unlock()
		return
	}
	u.loadMark = t.lastSeq.Load()
	u.held = nil
	u.loading = true
	u.mu.Unlock()
	start := time.Now()
	rooms, err := t.loader.Load(u.ctx, u.key.tenant, u.key.user)
	t.stats.observeLoad(time.Since(start))
	switch {
	case u.ctx.Err() != nil:
	case err != nil:
		t.loadFailed(u, gen, err)
	default:
		t.loaded(u, gen, rooms, reason)
	}
}

func (t *Table) loadFailed(u *user, gen uint64, err error) {
	t.stats.loadFailures.Add(1)
	t.failLog.Do(func() { t.log.Warn("load room ids failed; closing the connections of the user", "err", err) })
	s := t.userShard(u.key)
	s.mu.Lock()
	defer s.mu.Unlock()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone || u.gen != gen {
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
	u.held = nil
	for c := range u.conns {
		c.sink.Fail()
	}
}

func (t *Table) loaded(u *user, gen uint64, rooms []Room, reason chatimv1.ResyncReason) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone || u.gen != gen {
		return
	}
	u.loading = false
	if u.ready {
		t.replace(u, rooms, reason)
		return
	}
	u.mark = u.loadMark
	for _, r := range rooms {
		u.rooms[r.ID] = r.Ver
	}
	for _, ch := range u.held {
		if ch.seq > u.mark {
			u.apply(ch)
		}
	}
	u.held = nil
	for room := range u.rooms {
		t.index(u, room)
	}
	u.ready = true
	for c := range u.conns {
		c.becameReady()
	}
}

func (t *Table) replace(u *user, rooms []Room, reason chatimv1.ResyncReason) {
	old := u.rooms
	next := make(map[uint64]uint32, len(rooms))
	for _, r := range rooms {
		if gone, ok := u.left[r.ID]; ok && r.Ver <= gone {
			continue
		}
		next[r.ID] = r.Ver
		delete(u.left, r.ID)
	}
	u.rooms = next
	for _, ch := range u.held {
		if ch.seq > u.loadMark {
			u.apply(ch)
		}
	}
	u.held = nil
	for room, ver := range old {
		if _, kept := u.rooms[room]; !kept {
			u.left[room] = max(u.left[room], ver)
		}
	}
	u.mark = u.loadMark
	for room := range u.rooms {
		if _, had := old[room]; !had {
			t.index(u, room)
			for c := range u.conns {
				c.admitted(room)
			}
		}
	}
	for room := range old {
		if _, kept := u.rooms[room]; !kept {
			t.unindex(u, room)
			for c := range u.conns {
				c.removed(room)
			}
		}
	}
	t.stats.reloads.Add(1)
	for c := range u.conns {
		c.resync(reason)
	}
}
