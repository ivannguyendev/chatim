package interest

import (
	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/frame"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

type lazyFrame struct {
	data []byte
	out  []byte
}

func (f *lazyFrame) get() []byte {
	if f.out == nil {
		f.out = frame.Event(f.data)
	}
	return f.out
}

func (t *Table) Route(env feed.Envelope) {
	t.raiseSeq(env.Seq)
	r, ok := kinds[env.Kind]
	if !ok {
		t.stats.dropped[dropUnknownKind].Add(1)
		return
	}
	room, err := ids.ParseRoomID(env.Room)
	if err != nil || env.Tenant == "" || !r.accepts(env.DataKind) {
		t.stats.dropped[dropMalformed].Add(1)
		return
	}
	k := roomKey{tenant: env.Tenant, room: room}
	f := &lazyFrame{data: env.Data}
	switch r.audience {
	case toMembers:
		t.toMembers(k, f)
	case toViewers:
		t.toViewers(t.viewers(k), f, nil)
	case toOwner, toMembership:
		t.toUser(env, r, k, f)
	}
}

func (t *Table) toUser(env feed.Envelope, r rule, k roomKey, f *lazyFrame) {
	id, ver, ok := r.user(env)
	if !ok {
		t.stats.dropped[dropMalformed].Add(1)
		return
	}
	uk := userKey{tenant: env.Tenant, user: id}
	if r.audience == toOwner {
		t.toOwner(uk, f)
		return
	}
	t.membership(k, uk, change{seq: env.Seq, room: k.room, ver: ver, added: env.Kind == kindAdded}, f)
}

func (t *Table) raiseSeq(seq uint64) {
	for {
		cur := t.lastSeq.Load()
		if seq <= cur || t.lastSeq.CompareAndSwap(cur, seq) {
			return
		}
	}
}

func (t *Table) toMembers(k roomKey, f *lazyFrame) {
	for _, u := range t.members(k) {
		u.mu.Lock()
		if _, member := u.rooms[k.room]; member && u.ready && !u.gone {
			t.stats.delivered[levelMember].Add(u.send(f.get()))
		}
		u.mu.Unlock()
	}
}

func (t *Table) toViewers(viewers []*Conn, f *lazyFrame, skip *user) {
	for _, c := range viewers {
		if c.user == skip {
			continue
		}
		c.sink.Send(f.get())
		t.stats.delivered[levelOpen].Add(1)
	}
}

func (t *Table) toOwner(k userKey, f *lazyFrame) {
	u := t.lookup(k)
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ready && !u.gone {
		t.stats.delivered[levelOwner].Add(u.send(f.get()))
	}
}

func (t *Table) membership(k roomKey, uk userKey, ch change, f *lazyFrame) {
	viewers := t.viewers(k)
	var skip *user
	if u := t.lookup(uk); u != nil {
		u.mu.Lock()
		if t.applyMembership(u, ch, f) {
			skip = u
		}
		u.mu.Unlock()
	}
	t.toViewers(viewers, f, skip)
}

func (t *Table) applyMembership(u *user, ch change, f *lazyFrame) bool {
	switch {
	case u.gone:
		return false
	case !u.ready:
		u.held = append(u.held, ch)
		return false
	case ch.seq <= u.mark:
		return false
	case ch.added:
		applied, wasMember := u.applyAdded(ch.room, ch.ver)
		if !applied {
			return false
		}
		t.stats.delivered[levelOwner].Add(u.send(f.get()))
		if !wasMember {
			t.index(u, ch.room)
		}
		for c := range u.conns {
			c.admitted(ch.room)
		}
		return true
	default:
		applied, wasMember := u.applyRemoved(ch.room, ch.ver)
		if !applied {
			return false
		}
		t.stats.delivered[levelOwner].Add(u.send(f.get()))
		if wasMember {
			t.unindex(u, ch.room)
		}
		for c := range u.conns {
			c.removed(ch.room)
		}
		return true
	}
}
