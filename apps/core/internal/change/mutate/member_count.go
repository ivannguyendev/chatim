package mutate

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const settleTimeout = 2 * time.Second

func settling(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
}

func (m *Mutator) armCount(ctx context.Context, room uint64) (work.Timer, error) {
	t, err := m.d.Timers.Arm(ctx, room)
	if err != nil {
		m.d.Log.WarnContext(ctx, "member count check timer not armed; member command refused", "room", room, "err", err)
		return work.Timer{}, domain.ErrRetryLater
	}
	return t, nil
}

func (m *Mutator) settleCount(ctx context.Context, r domain.Room, t work.Timer, delta int, actor string, at time.Time) *chatimv1.Event {
	if delta == 0 {
		m.d.Timers.Disarm(ctx, t)
		return nil
	}
	c, err := m.d.Members.AddMemberCount(ctx, r.ID, delta)
	if err != nil {
		m.d.Log.WarnContext(ctx, "member count not updated; the check timer will recount", "room", r.ID, "delta", delta, "err", err)
		return nil
	}
	m.d.Timers.Disarm(ctx, t)
	return pbconv.MemberCountChanged(r, c, actor, at)
}

func (m *Mutator) announce(r domain.Room, written []domain.Member, count *chatimv1.Event) {
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
		_ = m.d.Events.Enqueue(r.ID, events)
	}
}

func (m *Mutator) maybeArm(ctx context.Context, room uint64, delta int) (work.Timer, error) {
	if delta == 0 {
		return work.Timer{}, nil
	}
	return m.armCount(ctx, room)
}
