package effects

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const PinProjectionName = "pin_projection"

type PinProjection struct {
	proj    PinProjecter
	dropped atomic.Uint64
}

func NewPinProjection(p PinProjecter) (*PinProjection, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: %s needs a pin projector", apperr.ErrInvalidArgument, PinProjectionName)
	}
	return &PinProjection{proj: p}, nil
}

func (e *PinProjection) Effect() Effect {
	return Effect{Name: PinProjectionName, Run: e.run}
}

func (e *PinProjection) Dropped() uint64 { return e.dropped.Load() }

func (e *PinProjection) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, recordRoom) {
		var target uint64
		for _, i := range g.indexes {
			target = max(target, recs[i].Seq)
		}
		_, err := e.proj.Project(ctx, g.key, target)
		switch {
		case errors.Is(err, domain.ErrRoomNotFound):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		}
	}
	return errs
}
