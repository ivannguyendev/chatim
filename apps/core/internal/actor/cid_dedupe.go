package actor

type dedupeKey struct{ user, cid string }

type dedupe struct {
	pending map[dedupeKey][]*request
	acks    *lru[dedupeKey, Ack]
}

func newDedupe(limit int) dedupe {
	return dedupe{pending: make(map[dedupeKey][]*request), acks: newLRU[dedupeKey, Ack](limit)}
}

func (d *dedupe) join(k dedupeKey, q *request) bool {
	if waiters, ok := d.pending[k]; ok {
		d.pending[k] = append(waiters, q)
		return true
	}
	if ack, ok := d.acks.get(k); ok {
		q.answer(ack, nil)
		return true
	}
	d.pending[k] = []*request{q}
	return false
}

func (d *dedupe) commit(k dedupeKey, ack Ack) {
	for _, q := range d.pending[k] {
		q.answer(ack, nil)
	}
	delete(d.pending, k)
	d.acks.put(k, ack)
}

func (d *dedupe) fail(k dedupeKey, err error) {
	for _, q := range d.pending[k] {
		q.answer(Ack{}, err)
	}
	delete(d.pending, k)
}

func (d *dedupe) seed(k dedupeKey, ack Ack) { d.acks.put(k, ack) }
