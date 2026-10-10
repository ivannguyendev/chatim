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

const CountRepairName = "count_repair"

var errMessageCountMoved = fmt.Errorf("message count moved during the recount: %w", domain.ErrRetryLater)

type CountRepairDeps struct {
	Messages     MessageFinder
	Interactions MessageCountReader
	Counts       MessageCountWriter
	Timers       MessageCountTimers
	Rooms        RoomReader
	JS           publish.JetStream
	Now          func() time.Time
}

type CountRepairConfig struct {
	SubjectRoot string
	RoomCache   int
}

type CountRepair struct {
	eventPublisher
	deps      CountRepairDeps
	reactions atomic.Uint64
	replies   atomic.Uint64
}

type countTarget struct {
	key     store.MsgKey
	counter string
}

type recount struct {
	same  bool
	ver   uint64
	next  domain.Message
	write func(ctx context.Context) (bool, error)
}

func NewCountRepair(deps CountRepairDeps, cfg CountRepairConfig) (*CountRepair, error) {
	if !deps.complete() || cfg.SubjectRoot == "" || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: %s needs messages, interactions, counts, timers, rooms, a jetstream client and a subject root", apperr.ErrInvalidArgument, CountRepairName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	e := &CountRepair{deps: deps}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cmp.Or(cfg.RoomCache, DefaultRoomCache))
	return e, nil
}

func (d CountRepairDeps) complete() bool {
	return d.Messages != nil && d.Interactions != nil && d.Counts != nil && d.Timers != nil && d.Rooms != nil && d.JS != nil
}

func (e *CountRepair) Effect() Effect {
	return Effect{Name: CountRepairName, Run: e.run}
}

func (e *CountRepair) Repaired(counter string) uint64 {
	switch counter {
	case pbconv.ReactionsCounter:
		return e.reactions.Load()
	case pbconv.RepliesCounter:
		return e.replies.Load()
	default:
		return 0
	}
}

func (e *CountRepair) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, countTargetOf) {
		err := e.repair(ctx, g.key)
		switch {
		case gone(err):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		}
	}
	return errs
}

func countTargetOf(r work.Record) countTarget {
	return countTarget{key: recordKey(r), counter: r.User}
}

func (e *CountRepair) repair(ctx context.Context, t countTarget) error {
	found, err := e.deps.Messages.Find(ctx, t.key.Room, []store.MsgKey{t.key})
	switch {
	case err != nil:
		return err
	case len(found) == 0:
		return domain.ErrMessageNotFound
	}
	typ, err := e.types.get(ctx, t.key.Room)
	if err != nil {
		return err
	}
	c, err := e.recount(ctx, t, found[0])
	switch {
	case err != nil:
		return err
	case c.same && c.ver == 0:
		return nil
	case c.same:
		return e.announce(ctx, typ, t.counter, found[0])
	}
	if _, err := e.deps.Timers.ArmMessageCountCheck(ctx, t.key, t.counter); err != nil {
		return err
	}
	ok, err := c.write(ctx)
	switch {
	case err != nil:
		return err
	case !ok:
		return errMessageCountMoved
	}
	e.repairedOf(t.counter).Add(1)
	return e.announce(ctx, typ, t.counter, c.next)
}

func (e *CountRepair) recount(ctx context.Context, t countTarget, m domain.Message) (recount, error) {
	next := m
	switch t.counter {
	case pbconv.ReactionsCounter:
		counts, err := e.deps.Interactions.CountReactions(ctx, t.key)
		if err != nil {
			return recount{}, err
		}
		base := m.Reactions.Version
		next.Reactions = domain.ReactionSummary{Counts: counts, Version: base + 1}
		write := func(ctx context.Context) (bool, error) {
			return e.deps.Counts.SetReactions(ctx, t.key, base, next.Reactions)
		}
		return recount{same: slices.Equal(counts, m.Reactions.Counts), ver: base, next: next, write: write}, nil
	case pbconv.RepliesCounter:
		n, err := e.deps.Interactions.CountLiveReplies(ctx, t.key)
		if err != nil {
			return recount{}, err
		}
		base := m.Replies.Version
		next.Replies = domain.ReplyCount{N: n, Version: base + 1}
		write := func(ctx context.Context) (bool, error) { return e.deps.Counts.SetReplyCount(ctx, t.key, base, n) }
		return recount{same: n == m.Replies.N, ver: base, next: next, write: write}, nil
	default:
		return recount{}, fmt.Errorf("%w: message counter %q", apperr.ErrInvalidArgument, t.counter)
	}
}

func (e *CountRepair) repairedOf(counter string) *atomic.Uint64 {
	if counter == pbconv.RepliesCounter {
		return &e.replies
	}
	return &e.reactions
}

func (e *CountRepair) announce(ctx context.Context, typ domain.RoomType, counter string, m domain.Message) error {
	at := e.deps.Now().UTC().Truncate(time.Millisecond)
	var ev *chatimv1.Event
	if counter == pbconv.RepliesCounter {
		ev = pbconv.ReplyCountsChanged(typ, m, at)
	} else {
		ev = pbconv.CountsChanged(typ, m, at)
	}
	errs := make([]error, 1)
	awaitAcks(ctx, e.queue(nil, errs, 0, m.Room, ev), errs, countStored(&e.republished))
	return errs[0]
}
