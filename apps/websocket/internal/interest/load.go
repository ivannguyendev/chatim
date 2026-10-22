package interest

import (
	"context"
	"time"
)

func (t *Table) load(ctx context.Context, u *user) {
	defer u.cancel()
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-t.slots }()
	u.mu.Lock()
	u.mark = t.lastSeq.Load()
	u.mu.Unlock()
	start := time.Now()
	rooms, err := t.loader.Load(ctx, u.key.tenant, u.key.user)
	t.stats.observeLoad(time.Since(start))
	switch {
	case ctx.Err() != nil:
	case err != nil:
		t.loadFailed(u, err)
	default:
		t.loaded(u, rooms)
	}
}

func (t *Table) loadFailed(u *user, err error) {
	t.stats.loadFailures.Add(1)
	t.failLog.Do(func() { t.log.Warn("load room ids failed; closing the connections of the user", "err", err) })
	s := t.userShard(u.key)
	s.mu.Lock()
	defer s.mu.Unlock()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone {
		return
	}
	u.gone = true
	if s.users[u.key] == u {
		delete(s.users, u.key)
		t.stats.users.Add(-1)
	}
	u.held = nil
	for c := range u.conns {
		c.sink.Fail()
	}
}

func (t *Table) loaded(u *user, rooms []Room) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone {
		return
	}
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
