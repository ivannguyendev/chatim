package sweep

import (
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const collectionSlots = int(store.StateRooms) + 1

type counters struct {
	oldest          [collectionSlots]atomic.Int64
	republished     [collectionSlots]atomic.Uint64
	dropped         [collectionSlots]atomic.Uint64
	publishFailures atomic.Uint64
	failures        atomic.Uint64
	passes          atomic.Uint64
}

func (s *Sweeper) PendingOldest(c store.StateCollection) time.Duration {
	if !c.Valid() {
		return 0
	}
	return time.Duration(s.stats.oldest[c].Load())
}

func (s *Sweeper) Republished(c store.StateCollection) uint64 {
	if !c.Valid() {
		return 0
	}
	return s.stats.republished[c].Load()
}

func (s *Sweeper) Dropped(c store.StateCollection) uint64 {
	if !c.Valid() {
		return 0
	}
	return s.stats.dropped[c].Load()
}

func (s *Sweeper) PublishFailures() uint64 { return s.stats.publishFailures.Load() }

func (s *Sweeper) Failures() uint64 { return s.stats.failures.Load() }

func (s *Sweeper) Passes() uint64 { return s.stats.passes.Load() }

func (c *counters) setOldest(coll store.StateCollection, now time.Time, docs []store.PendingDoc) {
	var age time.Duration
	if len(docs) > 0 {
		age = max(0, now.Sub(docs[0].StateAt))
	}
	c.oldest[coll].Store(int64(age))
}

func (c *counters) raiseOldest(coll store.StateCollection, now time.Time, docs []store.PendingDoc) {
	if len(docs) > 0 {
		age := int64(max(0, now.Sub(docs[0].StateAt)))
		c.oldest[coll].Store(max(age, c.oldest[coll].Load()))
	}
}

func (c *counters) resetOldest() {
	for i := range c.oldest {
		c.oldest[i].Store(0)
	}
}
