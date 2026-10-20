package member

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const settleTimeout = 2 * time.Second

func Settling(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
}

func (s *Service) ArmCount(ctx context.Context, room uint64) (timer.Timer, error) {
	t, err := s.d.Timers.ArmMemberCountCheck(ctx, room)
	if err != nil {
		s.d.Log.WarnContext(ctx, "member count check timer not armed; member command refused", "room", room, "err", err)
		return timer.Timer{}, domain.ErrRetryLater
	}
	return t, nil
}

func (s *Service) SettleCount(ctx context.Context, room uint64, t timer.Timer, delta int) (domain.MemberCount, bool) {
	if delta == 0 {
		s.d.Timers.Disarm(ctx, t)
		return domain.MemberCount{}, false
	}
	c, err := s.d.Members.AddMemberCount(ctx, room, delta)
	if err != nil {
		s.d.Log.WarnContext(ctx, "member count not updated; the check timer will recount", "room", room, "delta", delta, "err", err)
		return domain.MemberCount{}, false
	}
	s.d.Timers.Disarm(ctx, t)
	return c, true
}

func (s *Service) settleCount(ctx context.Context, r domain.Room, t timer.Timer, delta int, actor string, at time.Time) *chatimv1.Event {
	c, counted := s.SettleCount(ctx, r.ID, t, delta)
	if !counted {
		return nil
	}
	return pbconv.MemberCountChanged(r, c, actor, at)
}

func (s *Service) announce(r domain.Room, written []domain.Member, count *chatimv1.Event) {
	events := make([]*chatimv1.Event, 0, len(written)+1)
	for _, doc := range written {
		if ev := pbconv.MemberEvent(r.Type, doc); ev != nil {
			events = append(events, ev)
		}
	}
	if count != nil {
		events = append(events, count)
	}
	if len(events) > 0 {
		_ = s.d.Events.Enqueue(r.ID, events)
	}
}

func (s *Service) maybeArm(ctx context.Context, room uint64, delta int) (timer.Timer, error) {
	if delta == 0 {
		return timer.Timer{}, nil
	}
	return s.ArmCount(ctx, room)
}
