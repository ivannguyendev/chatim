package effects_test

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

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

func (brokenReactions) GetReaction(context.Context, store.MsgKey, string) (domain.Reaction, bool, error) {
	return domain.Reaction{}, false, errBoom
}

type brokenPins struct{}

func (brokenPins) At(context.Context, uint64, uint64) (domain.PinAction, error) {
	return domain.PinAction{}, errBoom
}
