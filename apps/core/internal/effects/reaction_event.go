package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReactionEventName = "reaction_event"

var errNoReaction = fmt.Errorf("reaction %w", apperr.ErrNotFound)

type ReactionEventDeps struct {
	Reactions ReactionReader
	Rooms     RoomReader
	JS        publish.JetStream
}

type ReactionEvent struct {
	eventPublisher
	reactions ReactionReader
	delay     time.Duration
}

func NewReactionEvent(deps ReactionEventDeps, cfg MessageChangedConfig) (*ReactionEvent, error) {
	if deps.Reactions == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs reactions, rooms and a jetstream client", apperr.ErrInvalidArgument, ReactionEventName)
	}
	cfg, err := eventConfig(ReactionEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &ReactionEvent{reactions: deps.Reactions, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *ReactionEvent) Effect() Effect {
	return Effect{Name: ReactionEventName, Delay: e.delay, Run: e.run}
}

func (e *ReactionEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, reactionGone)
}

func reactionGone(err error) bool { return gone(err) || errors.Is(err, errNoReaction) }

func (e *ReactionEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, found, err := e.reactions.Get(ctx, recordKey(r), r.User)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, errNoReaction
	case doc.N > r.Version:
		return nil, nil
	case doc.N < r.Version:
		return nil, store.ErrStaleRead
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.ReactionChanged(typ, doc), nil
}
