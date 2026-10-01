package actor

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type entry struct {
	key        dedupeKey
	msg        domain.Message
	admittedAt time.Time
	reassigns  int
	resends    int
	fixed      bool
	dup        bool
	reserved   bool
}

type group struct {
	ctx     context.Context
	cancel  context.CancelFunc
	entries []*entry
}

type actor struct {
	r       *Router
	id      uint64
	mailbox chan *request
	results chan []store.Result

	room     domain.Room
	last     uint64
	stale    bool
	dirty    bool
	markedAt time.Time
	members  *lru[string, struct{}]
	cache    cidCache
	retries  []*entry
	flight   *group
	landed   []landing
	failed   []failure
}

func newActor(r *Router, id uint64) *actor {
	return &actor{
		r:       r,
		id:      id,
		mailbox: make(chan *request, r.cfg.Mailbox),
		results: make(chan []store.Result, 1),
		dirty:   true,
		members: newLRU[string, struct{}](memberCacheSize),
		cache:   newCIDCache(cidCacheSize, cidCacheTTL),
	}
}

func (a *actor) offer(q *request) error {
	select {
	case a.mailbox <- q:
		return nil
	default:
		return errMailboxFull
	}
}

func (a *actor) deliver(res []store.Result) { a.results <- res }

func (a *actor) run(ctx context.Context) {
	if err := a.load(ctx); err != nil {
		a.exit(err)
		return
	}
	idle := time.NewTimer(a.r.cfg.Idle)
	defer idle.Stop()
	closing := a.r.closing
	for {
		if ctx.Err() != nil {
			a.exit(errStopped)
			return
		}
		var first *request
		if a.flight == nil && len(a.retries) == 0 {
			if closing == nil && len(a.mailbox) == 0 {
				a.exit(errStopped)
				return
			}
			idle.Reset(a.r.cfg.Idle)
			select {
			case first = <-a.mailbox:
			case <-idle.C:
				if a.r.evict(a) {
					return
				}
				continue
			case <-closing:
				closing = nil
				continue
			case <-ctx.Done():
				a.exit(errStopped)
				return
			}
		}
		if a.flight == nil {
			a.dispatch(ctx, first)
		}
		if a.flight == nil {
			continue
		}
		select {
		case res := <-a.results:
			a.settle(res)
		case <-ctx.Done():
			a.exit(errStopped)
			return
		}
	}
}

func (a *actor) exit(err error) {
	a.r.remove(a)
	if g := a.flight; g != nil {
		a.flight = nil
		g.cancel()
		for _, e := range g.entries {
			a.cache.fail(e.key, err)
		}
	}
	for _, e := range a.retries {
		a.cache.fail(e.key, err)
	}
	a.retries = nil
	for {
		select {
		case q := <-a.mailbox:
			q.answer(Ack{}, err)
		default:
			return
		}
	}
}

func (a *actor) commit(e *entry, stored domain.Message) {
	a.last = max(a.last, stored.Seq)
	a.landed = append(a.landed, landing{e: e, msg: stored, ack: ackOf(stored)})
}

func (a *actor) fail(e *entry, err error, uncertain bool) {
	if uncertain {
		a.dirty = true
	}
	a.failed = append(a.failed, failure{e: e, err: err, release: e.reserved && !uncertain})
}
