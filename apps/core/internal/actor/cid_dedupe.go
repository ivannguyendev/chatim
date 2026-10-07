package actor

import (
	"time"

	"github.com/ivannguyendev/chatim/pkg/lru"
)

type dedupeKey struct{ user, cid string }

type cachedAck struct {
	ack     Ack
	expires time.Time
}

type cidCache struct {
	pending map[dedupeKey][]*request
	acks    *lru.Cache[dedupeKey, cachedAck]
	ttl     time.Duration
}

func newCIDCache(limit int, ttl time.Duration) cidCache {
	return cidCache{pending: make(map[dedupeKey][]*request), acks: lru.New[dedupeKey, cachedAck](limit), ttl: ttl}
}

func (d *cidCache) join(k dedupeKey, q *request) bool {
	if waiters, ok := d.pending[k]; ok {
		d.pending[k] = append(waiters, q)
		return true
	}
	if ack, ok := d.committed(k); ok {
		q.answer(ack, nil)
		return true
	}
	d.pending[k] = []*request{q}
	return false
}

func (d *cidCache) committed(k dedupeKey) (Ack, bool) {
	c, ok := d.acks.Get(k)
	if !ok {
		return Ack{}, false
	}
	if !time.Now().Before(c.expires) {
		d.acks.Remove(k)
		return Ack{}, false
	}
	return c.ack, true
}

func (d *cidCache) commit(k dedupeKey, ack Ack) {
	for _, q := range d.pending[k] {
		q.answer(ack, nil)
	}
	delete(d.pending, k)
	d.seed(k, ack)
}

func (d *cidCache) fail(k dedupeKey, err error) {
	for _, q := range d.pending[k] {
		q.answer(Ack{}, err)
	}
	delete(d.pending, k)
}

func (d *cidCache) seed(k dedupeKey, ack Ack) {
	d.acks.Put(k, cachedAck{ack: ack, expires: time.Now().Add(d.ttl)})
}
