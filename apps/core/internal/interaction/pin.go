package interaction

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type PinProjector interface {
	Current(ctx context.Context, room uint64) (domain.PinState, error)
	Project(ctx context.Context, room, target uint64) (domain.PinState, error)
}

type PinCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

func (s *Service) Pin(ctx context.Context, c PinCmd) (domain.PinState, error) {
	return s.pin(ctx, c, domain.PinOpPin, access.PinMessage)
}

func (s *Service) Unpin(ctx context.Context, c PinCmd) (domain.PinState, error) {
	return s.pin(ctx, c, domain.PinOpUnpin, access.UnpinMessage)
}

func (s *Service) pin(ctx context.Context, c PinCmd, op domain.PinOp, action access.Action) (domain.PinState, error) {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return domain.PinState{}, err
	}
	grant, msg, err := s.d.Access.Target(ctx, s.d.Messages, action, c.Tenant, c.User, key)
	if err != nil {
		return domain.PinState{}, err
	}
	if op == domain.PinOpPin && msg.Deleted {
		return domain.PinState{}, domain.ErrMessageDeleted
	}
	want := domain.PinAction{Room: key.Room, Tenant: c.Tenant, Op: op, Thread: key.Thread, Seq: key.Seq, By: c.User, At: s.now()}
	state, fact, wrote, err := s.commitPin(ctx, want)
	if err != nil || !wrote {
		return state, err
	}
	return s.finishPin(ctx, grant.Room.Type, msg, state, fact), nil
}

func (s *Service) commitPin(ctx context.Context, want domain.PinAction) (domain.PinState, domain.PinAction, bool, error) {
	state, err := s.d.Projector.Current(ctx, want.Room)
	if err != nil {
		return domain.PinState{}, domain.PinAction{}, false, err
	}
	if state.Pinned(want.Thread, want.Seq) == (want.Op == domain.PinOpPin) {
		return state, domain.PinAction{}, false, nil
	}
	if want.Op == domain.PinOpPin && len(state.Pins) >= s.d.Limits.PinLimit {
		return domain.PinState{}, domain.PinAction{}, false, domain.ErrTooManyPins
	}
	fact := want
	fact.PV = state.Version + 1
	got, err := s.appendPin(ctx, fact)
	switch {
	case err != nil:
		return domain.PinState{}, domain.PinAction{}, false, err
	case got.PV == 0:
		return domain.PinState{}, domain.PinAction{}, false, domain.ErrRetryLater
	}
	return state, got, true, nil
}

func (s *Service) appendPin(ctx context.Context, fact domain.PinAction) (domain.PinAction, error) {
	err := s.d.Pins.Append(ctx, fact)
	if !errors.Is(err, store.ErrPinExists) {
		return fact, err
	}
	got, err := s.d.Pins.At(ctx, fact.Room, fact.PV)
	if err != nil {
		return domain.PinAction{}, err
	}
	if got.Op != fact.Op || got.Thread != fact.Thread || got.Seq != fact.Seq || got.By != fact.By {
		return domain.PinAction{}, nil
	}
	return got, nil
}

func (s *Service) finishPin(ctx context.Context, typ domain.RoomType, msg domain.Message, before domain.PinState, fact domain.PinAction) domain.PinState {
	state, err := s.d.Projector.Project(ctx, fact.Room, fact.PV)
	if err != nil {
		return domain.FoldPins(before, []domain.PinAction{fact})
	}
	_ = s.d.Events.Enqueue(fact.Room, []*chatimv1.Event{pbconv.PinChanged(typ, msg, fact)})
	return state
}
