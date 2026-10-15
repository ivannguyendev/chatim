package effects

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const EditProjectionName = "edit_projection"

type EditProjectionDeps struct {
	Edits    EditReader
	Messages EditApplier
	Purger   TextPurger
}

type EditProjection struct {
	deps    EditProjectionDeps
	dropped atomic.Uint64
}

func NewEditProjection(deps EditProjectionDeps) (*EditProjection, error) {
	if deps.Edits == nil || deps.Messages == nil || deps.Purger == nil {
		return nil, fmt.Errorf("%w: edit_projection needs edits, messages and a text purger", apperr.ErrInvalidArgument)
	}
	return &EditProjection{deps: deps}, nil
}

func (e *EditProjection) Effect() Effect {
	return Effect{Name: EditProjectionName, Run: e.run}
}

func (e *EditProjection) Dropped() uint64 { return e.dropped.Load() }

func (e *EditProjection) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for i, r := range recs {
		err := e.apply(ctx, r)
		switch {
		case eventkit.Gone(err):
			e.dropped.Add(1)
		case err != nil:
			errs[i] = err
		}
	}
	return errs
}

func (e *EditProjection) apply(ctx context.Context, r work.Record) error {
	if r.Version == 0 {
		return nil
	}
	key := recordKey(r)
	fact, err := e.deps.Edits.At(ctx, key, r.Version)
	if err != nil {
		return err
	}
	if err := e.deps.Messages.ApplyEdit(ctx, fact); err != nil {
		return err
	}
	if fact.Kind != domain.EditDelete {
		return nil
	}
	return e.deps.Purger.PurgeText(ctx, key, fact.Version-1)
}

func recordKey(r work.Record) store.MsgKey {
	return eventkit.MessageKey(r.Room, r.Thread, r.Seq)
}
