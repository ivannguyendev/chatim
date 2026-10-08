package effects

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	ReactionCounterName = "reaction_counter"
	DefaultCountDelay   = time.Second
	DefaultCounterTries = 3
)

type ReactionCounterDeps struct {
	Messages MessageFinder
	Counter  CounterToucher
	Rooms    RoomReader
	JS       publish.JetStream
	Now      func() time.Time
}

type ReactionCounterConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
	Tries       int
}

type ReactionCounter struct {
	eventPublisher
	deps     ReactionCounterDeps
	cfg      ReactionCounterConfig
	repaired atomic.Uint64
}

func NewReactionCounter(deps ReactionCounterDeps, cfg ReactionCounterConfig) (*ReactionCounter, error) {
	if deps.Messages == nil || deps.Counter == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs messages, a counter, rooms and a jetstream client", apperr.ErrInvalidArgument, ReactionCounterName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultCountDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	cfg.Tries = cmp.Or(cfg.Tries, DefaultCounterTries)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 || cfg.Tries < 1 {
		return nil, fmt.Errorf("%w: %s config %+v needs a subject root, a delay, a room cache and a try", apperr.ErrInvalidArgument, ReactionCounterName, cfg)
	}
	c := &ReactionCounter{deps: deps, cfg: cfg}
	c.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return c, nil
}

func (c *ReactionCounter) Effect() Effect {
	return Effect{Name: ReactionCounterName, Delay: c.cfg.Delay, Run: c.run}
}

func (c *ReactionCounter) Repaired() uint64 { return c.repaired.Load() }

func (c *ReactionCounter) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	groups := groupRecords(recs, recordKey)
	var pending []pendingAck
	for _, g := range groups {
		ev, err := c.touch(ctx, g.key, witnessesOf(recs, g.indexes))
		switch {
		case gone(err):
			g.drop(&c.dropped)
		case err != nil:
			g.fail(errs, err)
		case ev != nil:
			pending = c.queue(pending, errs, g.indexes[0], g.key.Room, ev)
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&c.republished))
	for _, g := range groups {
		g.share(errs)
	}
	return errs
}

func (c *ReactionCounter) touch(ctx context.Context, key store.MsgKey, witnesses []store.Witness) (*chatimv1.Event, error) {
	found, err := c.deps.Messages.Find(ctx, key.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	}
	typ, err := c.types.get(ctx, key.Room)
	if err != nil {
		return nil, err
	}
	msg := found[0]
	summary, bumped, err := c.deps.Counter.Touch(ctx, key, msg.Reactions, witnesses, c.cfg.Tries)
	if err != nil {
		return nil, err
	}
	if bumped {
		c.repaired.Add(1)
	}
	if summary.Version == 0 {
		return nil, nil
	}
	msg.Reactions = summary
	return pbconv.CountsChanged(typ, msg, c.deps.Now().UTC().Truncate(time.Millisecond)), nil
}

func witnessesOf(recs []work.Record, indexes []int) []store.Witness {
	var out []store.Witness
	for _, i := range indexes {
		r := recs[i]
		j := slices.IndexFunc(out, func(w store.Witness) bool { return w.User == r.User })
		if j < 0 {
			out = append(out, store.Witness{User: r.User, N: r.Version})
			continue
		}
		out[j].N = max(out[j].N, r.Version)
	}
	return out
}
