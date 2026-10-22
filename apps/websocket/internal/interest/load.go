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
	m, err := t.loader.Load(u.ctx, u.key.tenant, u.key.user)
	t.stats.load.observe(time.Since(start))
	switch {
	case u.ctx.Err() != nil:
	case err != nil:
		if t.loadFailed(u, gen, err) {
			t.requeue(u, reason)
		}
	default:
		t.loaded(u, gen, m, reason)
	}
}

func (t *Table) loadFailed(u *user, gen uint64, err error) bool {
	s := t.userShard(u.key)
	s.mu.Lock()
	defer s.mu.Unlock()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone || u.gen != gen {
		return false
	}
	u.loading = false
	u.held = nil
	if u.suspended && u.readied() {
		t.stats.reloadFailures.Add(1)
		t.failLog.Do(func() {
			t.log.Warn("reload room ids failed; the user stays suspended and is reloaded again after a pause", "err", err)
		})
		return true
	}
	t.stats.loadFailures.Add(1)
	t.failLog.Do(func() { t.log.Warn("load room ids failed; closing the connections of the user", "err", err) })
	t.drop(s, u)
	for c := range u.conns {
		c.sink.Fail()
	}
	return false
}

func (t *Table) loaded(u *user, gen uint64, m Membership, reason chatimv1.ResyncReason) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone || u.gen != gen {
		return
	}
	u.loading = false
	t.adopt(u, m)
	u.ready = true
	if since := u.suspendedAt; t.resume(u) {
		t.stats.reloads.Add(1)
		t.stats.reload.observe(time.Since(since))
	}
	for c := range u.conns {
		if c.readied {
			c.resumed(reason)
			continue
		}
		c.becameReady()
	}
}

func (t *Table) adopt(u *user, m Membership) {
	old := u.rooms
	if !u.ready {
		old = nil
	}
	for _, r := range m.Left {
		u.left[r.ID] = max(u.left[r.ID], r.Ver)
	}
	next := make(map[uint64]uint32, len(m.Joined))
	for _, r := range m.Joined {
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
}
