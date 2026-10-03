package publish

type batch struct {
	room uint64
	open int
}

type roomQueues struct {
	active  map[uint64]*batch
	waiting map[uint64][]item
	held    int
}

func newRoomQueues() roomQueues {
	return roomQueues{active: make(map[uint64]*batch), waiting: make(map[uint64][]item)}
}

func (q *roomQueues) empty() bool { return len(q.active) == 0 && q.held == 0 }

func (q *roomQueues) hold(it item) bool {
	if _, busy := q.active[it.room]; !busy {
		return false
	}
	q.waiting[it.room] = append(q.waiting[it.room], it)
	q.held += len(it.events)
	return true
}

func (q *roomQueues) start(b *batch) { q.active[b.room] = b }

func (q *roomQueues) finish(room uint64) (item, bool) {
	delete(q.active, room)
	items := q.waiting[room]
	if len(items) == 0 {
		return item{}, false
	}
	it := items[0]
	items[0] = item{}
	if len(items) == 1 {
		delete(q.waiting, room)
	} else {
		q.waiting[room] = items[1:]
	}
	q.held -= len(it.events)
	return it, true
}
