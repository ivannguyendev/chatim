package memstore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.EventStates = (*EventStates)(nil)

type stateSource interface {
	pendingDocs(c store.StateCollection) []pendingEntry
	applyStates(targets []store.StateTarget, mark bool, at time.Time)
	seedState(target store.StateTarget, at time.Time) error
}

type EventStateStores struct {
	Messages     *Messages
	Rooms        *Rooms
	Edits        *Edits
	Hidden       *Hidden
	Interactions *Interactions
	Pins         *Pins
}

type EventStates struct {
	sources map[store.StateCollection]stateSource
	now     func() time.Time
}

func NewEventStates(s EventStateStores) *EventStates {
	e := &EventStates{sources: make(map[store.StateCollection]stateSource), now: time.Now}
	if s.Messages != nil {
		e.sources[store.StateMessages] = s.Messages
	}
	if s.Rooms != nil {
		e.sources[store.StateRooms], e.sources[store.StateMembers] = s.Rooms, s.Rooms
	}
	if s.Edits != nil {
		e.sources[store.StateEdits] = s.Edits
	}
	if s.Hidden != nil {
		e.sources[store.StateHidden] = s.Hidden
	}
	if s.Interactions != nil {
		e.sources[store.StateInteractions] = s.Interactions
	}
	if s.Pins != nil {
		e.sources[store.StatePins] = s.Pins
	}
	return e
}

func (e *EventStates) Pending(ctx context.Context, c store.StateCollection, from, before time.Time, after store.PendingCursor, limit int) ([]store.PendingDoc, store.PendingCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, store.PendingCursor{}, err
	}
	if err := store.ValidatePendingScan(c, limit); err != nil {
		return nil, store.PendingCursor{}, err
	}
	src, ok := e.sources[c]
	if !ok {
		return nil, store.PendingCursor{}, nil
	}
	from, before = millis(from), millis(before)
	entries := slices.DeleteFunc(src.pendingDocs(c), func(p pendingEntry) bool {
		return p.doc.StateAt.Before(from) || !p.doc.StateAt.Before(before) ||
			(!after.IsZero() && comparePending(p, after.At, after.Key) <= 0)
	})
	slices.SortFunc(entries, func(a, b pendingEntry) int { return comparePending(a, b.doc.StateAt, b.key) })
	var next store.PendingCursor
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[limit-1]
		next = store.PendingCursor{At: last.doc.StateAt, Key: last.key}
	}
	out := make([]store.PendingDoc, len(entries))
	for i, p := range entries {
		out[i] = p.doc
	}
	return out, next, nil
}

func (e *EventStates) Clear(ctx context.Context, targets []store.StateTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateStateTargets(targets); err != nil {
		return err
	}
	for _, c := range store.AllStateCollections() {
		group := slices.DeleteFunc(slices.Clone(targets), func(t store.StateTarget) bool { return t.Collection != c })
		if src, ok := e.sources[c]; ok && len(group) > 0 {
			src.applyStates(group, false, time.Time{})
		}
	}
	return nil
}

func (e *EventStates) Mark(ctx context.Context, c store.StateCollection, targets []store.StateTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateMarkTargets(c, targets); err != nil {
		return err
	}
	if src, ok := e.sources[c]; ok && len(targets) > 0 {
		src.applyStates(targets, true, e.now())
	}
	return nil
}

func (e *EventStates) SeedState(ctx context.Context, target store.StateTarget, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if err := store.ValidateMarkTime(at); err != nil {
		return err
	}
	src, ok := e.sources[target.Collection]
	if !ok {
		return fmt.Errorf("seed state of %s: no store", target.Collection)
	}
	return src.seedState(target, at)
}
