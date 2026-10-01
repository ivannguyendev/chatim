package actor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Router struct {
	msgs  store.Messages
	rooms store.Rooms
	sub   Submitter
	cids  CIDRegistry
	cfg   Config
	log   *slog.Logger

	mu      sync.RWMutex
	actors  map[uint64]*actor
	started bool
	closed  bool
	runCtx  context.Context
	wg      sync.WaitGroup
	closing chan struct{}
	done    chan struct{}
}

func NewRouter(msgs store.Messages, rooms store.Rooms, sub Submitter, cids CIDRegistry, cfg Config, log *slog.Logger) (*Router, error) {
	if msgs == nil || rooms == nil || sub == nil || cids == nil {
		return nil, fmt.Errorf("%w: router needs message and room stores, a submitter and a cid registry", apperr.ErrInvalidArgument)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		msgs:    msgs,
		rooms:   rooms,
		sub:     sub,
		cids:    cids,
		cfg:     cfg,
		log:     log,
		actors:  make(map[uint64]*actor),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}, nil
}

func (r *Router) Run(ctx context.Context) error {
	if err := r.start(ctx); err != nil {
		return err
	}
	defer close(r.done)
	select {
	case <-ctx.Done():
	case <-r.closing:
	}
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.wg.Wait()
	return ctx.Err()
}

func (r *Router) start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return errStarted
	}
	r.started, r.runCtx = true, ctx
	return nil
}

func (r *Router) Close(ctx context.Context) error {
	r.mu.Lock()
	started := r.started
	if !r.closed {
		r.closed = true
		close(r.closing)
	}
	r.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Router) Send(ctx context.Context, c SendCmd) (Ack, error) {
	if err := c.validate(); err != nil {
		return Ack{}, err
	}
	if err := ctx.Err(); err != nil {
		return Ack{}, err
	}
	q := newRequest(c)
	if err := r.enqueue(q); err != nil {
		return Ack{}, err
	}
	select {
	case out := <-q.reply:
		return out.ack, out.err
	case <-ctx.Done():
		return Ack{}, ctx.Err()
	}
}

func (r *Router) enqueue(q *request) error {
	if routed, err := r.offerExisting(q); routed {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.closed {
		return errNotRunning
	}
	a := r.actors[q.cmd.Room]
	if a == nil {
		if len(r.actors) >= r.cfg.MaxActors {
			return errTooManyRooms
		}
		a = newActor(r, q.cmd.Room)
		r.actors[a.id] = a
		ctx := r.runCtx
		r.wg.Go(func() { a.run(ctx) })
	}
	return a.offer(q)
}

func (r *Router) offerExisting(q *request) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.started || r.closed {
		return true, errNotRunning
	}
	a := r.actors[q.cmd.Room]
	if a == nil {
		return false, nil
	}
	return true, a.offer(q)
}

func (r *Router) evict(a *actor) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(a.mailbox) > 0 {
		return false
	}
	r.forgetLocked(a)
	return true
}

func (r *Router) remove(a *actor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgetLocked(a)
}

func (r *Router) forgetLocked(a *actor) {
	if r.actors[a.id] == a {
		delete(r.actors, a.id)
	}
}
