package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Pins = (*Pins)(nil)

type Pins struct {
	mu    sync.RWMutex
	facts map[uint64][]domain.PinAction
	log   *Messages
}

func NewPins() *Pins { return &Pins{facts: make(map[uint64][]domain.PinAction)} }

func (s *Pins) Append(ctx context.Context, a domain.PinAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidatePinAction(a); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.facts[a.Room]
	i, found := slices.BinarySearchFunc(line, a.PV, byPV)
	if found {
		return fmt.Errorf("append pin v%d of room %d: %w", a.PV, a.Room, store.ErrPinExists)
	}
	s.facts[a.Room] = slices.Insert(line, i, a)
	if s.log != nil {
		s.log.appendFact(logged{kind: store.PinInserted, pin: a})
	}
	return nil
}

func (s *Pins) At(ctx context.Context, room, pv uint64) (domain.PinAction, error) {
	if err := ctx.Err(); err != nil {
		return domain.PinAction{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[room]
	if i, ok := slices.BinarySearchFunc(line, pv, byPV); ok {
		return line[i], nil
	}
	return domain.PinAction{}, fmt.Errorf("pin v%d of room %d: %w", pv, room, store.ErrPinNotFound)
}

func (s *Pins) After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[room]
	i, found := slices.BinarySearchFunc(line, pv, byPV)
	if found {
		i++
	}
	return slices.Clone(line[i:min(len(line), i+limit)]), nil
}

func (s *Pins) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.PinAction{}
	for _, a := range s.facts[room] {
		if !a.At.Before(from) && !a.At.After(to) {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b domain.PinAction) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.PV, b.PV)) })
	return out[:min(len(out), limit)], nil
}

func byPV(a domain.PinAction, pv uint64) int { return cmp.Compare(a.PV, pv) }
