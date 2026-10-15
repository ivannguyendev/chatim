package timer

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMessageCountMoved = fmt.Errorf("message count moved during the recount: %w", domain.ErrRetryLater)

type CountRepairDeps struct {
	Messages     MessageFinder
	Interactions MessageCountReader
	Counts       MessageCountWriter
	Timers       MessageTimers
	Rooms        RoomReader
	Events       Events
	Now          func() time.Time
}

type CountRepair struct {
	deps      CountRepairDeps
	reactions atomic.Uint64
	replies   atomic.Uint64
}

type recount struct {
	same  bool
	ver   uint64
	next  domain.Message
	write func(ctx context.Context) (bool, error)
}

func NewCountRepair(deps CountRepairDeps) (*CountRepair, error) {
	if deps.Messages == nil || deps.Interactions == nil || deps.Counts == nil || deps.Timers == nil || deps.Rooms == nil || deps.Events == nil {
		return nil, fmt.Errorf("%w: message count repair needs messages, interactions, counts, timers, rooms and events", apperr.ErrInvalidArgument)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &CountRepair{deps: deps}, nil
}

func (r *CountRepair) Repaired(counter string) uint64 {
	switch counter {
	case pbconv.ReactionsCounter:
		return r.reactions.Load()
	case pbconv.RepliesCounter:
		return r.replies.Load()
	default:
		return 0
	}
}

func (r *CountRepair) Repair(ctx context.Context, c Check) error {
	found, err := r.deps.Messages.Find(ctx, c.Room, []store.MsgKey{c.Key()})
	switch {
	case err != nil:
		return err
	case len(found) == 0:
		return domain.ErrMessageNotFound
	}
	room, err := r.deps.Rooms.Get(ctx, c.Room)
	if err != nil {
		return err
	}
	rc, err := r.recount(ctx, c, found[0])
	switch {
	case err != nil:
		return err
	case rc.same && rc.ver == 0:
		return nil
	case rc.same:
		return r.announce(room.Type, c.Counter, found[0])
	}
	if _, err := r.deps.Timers.ArmMessageCountCheck(ctx, c.Key(), c.Counter); err != nil {
		return err
	}
	ok, err := rc.write(ctx)
	switch {
	case err != nil:
		return err
	case !ok:
		return errMessageCountMoved
	}
	r.repairedOf(c.Counter).Add(1)
	return r.announce(room.Type, c.Counter, rc.next)
}

func (r *CountRepair) recount(ctx context.Context, c Check, m domain.Message) (recount, error) {
	key, next := c.Key(), m
	switch c.Counter {
	case pbconv.ReactionsCounter:
		counts, err := r.deps.Interactions.CountReactions(ctx, key)
		if err != nil {
			return recount{}, err
		}
		base := m.Reactions.Version
		next.Reactions = domain.ReactionSummary{Counts: counts, Version: base + 1}
		write := func(ctx context.Context) (bool, error) {
			return r.deps.Counts.SetReactions(ctx, key, base, next.Reactions)
		}
		return recount{same: !m.Reactions.Unsettled && slices.Equal(counts, m.Reactions.Counts), ver: base, next: next, write: write}, nil
	case pbconv.RepliesCounter:
		n, err := r.deps.Interactions.CountLiveReplies(ctx, key)
		if err != nil {
			return recount{}, err
		}
		base := m.Replies.Version
		next.Replies = domain.ReplyCount{N: n, Version: base + 1}
		write := func(ctx context.Context) (bool, error) { return r.deps.Counts.SetReplyCount(ctx, key, base, n) }
		return recount{same: !m.Replies.Unsettled && n == m.Replies.N, ver: base, next: next, write: write}, nil
	default:
		return recount{}, fmt.Errorf("%w: message counter %q", apperr.ErrInvalidArgument, c.Counter)
	}
}

func (r *CountRepair) repairedOf(counter string) *atomic.Uint64 {
	if counter == pbconv.RepliesCounter {
		return &r.replies
	}
	return &r.reactions
}

func (r *CountRepair) announce(typ domain.RoomType, counter string, m domain.Message) error {
	at := stamp(r.deps.Now)
	ev := pbconv.CountsChanged(typ, m, at)
	if counter == pbconv.RepliesCounter {
		ev = pbconv.ReplyCountsChanged(typ, m, at)
	}
	return r.deps.Events.Enqueue(m.Room, []*chatimv1.Event{ev})
}
