package effects_test

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type touchCall struct {
	key       store.MsgKey
	witnesses []store.Witness
	tries     int
}

func sameTouch(a, b touchCall) bool {
	return a.key == b.key && a.tries == b.tries && slices.Equal(a.witnesses, b.witnesses)
}

type spyCounter struct {
	inner effects.CounterToucher
	err   error
	calls []touchCall
}

func (s *spyCounter) Touch(ctx context.Context, k store.MsgKey, cur domain.ReactionSummary, ws []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	s.calls = append(s.calls, touchCall{key: k, witnesses: slices.Clone(ws), tries: tries})
	if s.err != nil {
		return domain.ReactionSummary{}, false, s.err
	}
	return s.inner.Touch(ctx, k, cur, ws, tries)
}

type projectCall struct{ room, target uint64 }

type spyProjector struct {
	inner effects.PinProjecter
	fail  map[uint64]error
	calls []projectCall
}

func (s *spyProjector) Project(ctx context.Context, r, target uint64) (domain.PinState, error) {
	s.calls = append(s.calls, projectCall{room: r, target: target})
	if err := s.fail[r]; err != nil {
		return domain.PinState{}, err
	}
	return s.inner.Project(ctx, r, target)
}

type brokenReactions struct{}

func (brokenReactions) Get(context.Context, store.MsgKey, string) (domain.Reaction, bool, error) {
	return domain.Reaction{}, false, errBoom
}

type brokenPins struct{}

func (brokenPins) At(context.Context, uint64, uint64) (domain.PinAction, error) {
	return domain.PinAction{}, errBoom
}
