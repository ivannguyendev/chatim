package pinproj

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxTries = 5

type Facts interface {
	After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error)
}

var (
	ErrContended = fmt.Errorf("pin projection contended: %w", apperr.ErrUnavailable)

	errMissingDeps = fmt.Errorf("%w: pin projector needs facts and rooms", apperr.ErrInvalidArgument)
)

type Projector struct {
	facts Facts
	rooms store.PinProjector
}

func New(facts Facts, rooms store.PinProjector) (*Projector, error) {
	if facts == nil || rooms == nil {
		return nil, errMissingDeps
	}
	return &Projector{facts: facts, rooms: rooms}, nil
}

func (p *Projector) Current(ctx context.Context, room uint64) (domain.PinState, error) {
	s, err := p.rooms.PinState(ctx, room)
	if err != nil {
		return domain.PinState{}, fmt.Errorf("current pins of room %d: %w", room, err)
	}
	return p.fold(ctx, room, s)
}

func (p *Projector) Project(ctx context.Context, room, target uint64) (domain.PinState, error) {
	for range MaxTries {
		base, err := p.rooms.PinState(ctx, room)
		if err != nil {
			return domain.PinState{}, fmt.Errorf("project pins of room %d: %w", room, err)
		}
		if base.Version >= target {
			return base, nil
		}
		folded, err := p.fold(ctx, room, base)
		if err != nil {
			return domain.PinState{}, err
		}
		if folded.Version < target {
			return domain.PinState{}, fmt.Errorf("pins of room %d reach v%d, want v%d: %w", room, folded.Version, target, store.ErrStaleRead)
		}
		ok, err := p.rooms.ApplyPins(ctx, room, base.Version, folded)
		if err != nil {
			return domain.PinState{}, fmt.Errorf("project pins of room %d: %w", room, err)
		}
		if ok {
			return folded, nil
		}
	}
	return domain.PinState{}, fmt.Errorf("pins of room %d to v%d: %w", room, target, ErrContended)
}

func (p *Projector) fold(ctx context.Context, room uint64, s domain.PinState) (domain.PinState, error) {
	for {
		facts, err := p.facts.After(ctx, room, s.Version, store.MaxPinScan)
		if err != nil {
			return domain.PinState{}, fmt.Errorf("pin facts of room %d after v%d: %w", room, s.Version, err)
		}
		next := domain.FoldPins(s, facts)
		if len(facts) < store.MaxPinScan || next.Version == s.Version {
			return next, nil
		}
		s = next
	}
}
