package publish

import "time"

type track struct {
	wm     uint64
	synced uint64
	lowest uint64
	based  bool
	frozen bool
	open   int
	seen   time.Time
	ahead  map[uint64]struct{}
}

func (t *track) clean() bool { return t.based && t.wm <= t.synced }

func (t *track) forgettable() bool { return t.clean() && t.open == 0 }

type tracker struct {
	rooms     map[uint64]*track
	dirty     map[uint64]struct{}
	maxRooms  int
	maxAhead  int
	idle      time.Duration
	recheck   time.Duration
	fullUntil time.Time
	lastSweep time.Time
}

func newTracker(cfg Config) *tracker {
	return &tracker{
		rooms:    make(map[uint64]*track),
		dirty:    make(map[uint64]struct{}),
		maxRooms: cfg.MaxRooms,
		maxAhead: cfg.MaxAhead,
		idle:     cfg.RoomIdle,
		recheck:  cfg.FlushEvery,
	}
}

func (k *tracker) hand(room, pts uint64, now time.Time) bool {
	t := k.rooms[room]
	if t == nil {
		if !k.makeRoom(now) {
			return false
		}
		t = &track{lowest: pts}
		k.rooms[room] = t
		k.dirty[room] = struct{}{}
	}
	t.open++
	t.seen = now
	if !t.based {
		t.lowest = min(t.lowest, pts)
	}
	return true
}

func (k *tracker) makeRoom(now time.Time) bool {
	if len(k.rooms) < k.maxRooms {
		return true
	}
	if now.Before(k.fullUntil) {
		return false
	}
	for id, t := range k.rooms {
		if t.forgettable() {
			k.forget(id)
			return true
		}
	}
	k.fullUntil = now.Add(k.recheck)
	return false
}

func (k *tracker) acked(room, pts uint64) bool {
	t := k.rooms[room]
	t.open--
	if t.frozen || (t.based && pts <= t.wm) {
		return false
	}
	if t.ahead == nil {
		t.ahead = make(map[uint64]struct{})
	}
	t.ahead[pts] = struct{}{}
	if t.based {
		k.advance(room, t)
	}
	if len(t.ahead) > k.maxAhead {
		t.ahead, t.frozen = nil, true
		return true
	}
	return false
}

func (k *tracker) dropped(room uint64) { k.rooms[room].open-- }

func (k *tracker) advance(room uint64, t *track) {
	for {
		if _, ok := t.ahead[t.wm+1]; !ok {
			break
		}
		delete(t.ahead, t.wm+1)
		t.wm++
	}
	if !t.clean() {
		k.dirty[room] = struct{}{}
	}
}

func (k *tracker) due() []syncReq {
	reqs := make([]syncReq, 0, len(k.dirty))
	for room := range k.dirty {
		switch t := k.rooms[room]; {
		case t == nil || t.clean():
			delete(k.dirty, room)
		case !t.based:
			reqs = append(reqs, syncReq{room: room, init: true, value: t.lowest - 1})
		default:
			reqs = append(reqs, syncReq{room: room, value: t.wm})
		}
	}
	return reqs
}

func (k *tracker) settle(room, value uint64) {
	t := k.rooms[room]
	if t == nil {
		return
	}
	if t.based {
		t.wm = max(t.wm, value)
	} else {
		t.based, t.wm = true, value
	}
	t.synced = max(t.synced, value)
	for p := range t.ahead {
		if p <= t.wm {
			delete(t.ahead, p)
		}
	}
	k.advance(room, t)
	if t.clean() {
		delete(k.dirty, room)
	}
}

func (k *tracker) sweep(now time.Time) bool {
	if now.Sub(k.lastSweep) < k.idle/2 {
		return false
	}
	k.lastSweep = now
	for id, t := range k.rooms {
		if t.forgettable() && len(t.ahead) == 0 && now.Sub(t.seen) >= k.idle {
			k.forget(id)
		}
	}
	return len(k.rooms) < k.maxRooms
}

func (k *tracker) forget(room uint64) {
	delete(k.rooms, room)
	delete(k.dirty, room)
}
