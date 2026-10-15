package timer

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMemberCountMoved = fmt.Errorf("member count moved during the recount: %w", domain.ErrRetryLater)

type MemberCountRepairDeps struct {
	Rooms  RoomReader
	Counts MemberCounter
	Timers MemberTimers
	Events Events
	Now    func() time.Time
}

type MemberCountRepair struct {
	deps     MemberCountRepairDeps
	repaired atomic.Uint64
}

func NewMemberCountRepair(deps MemberCountRepairDeps) (*MemberCountRepair, error) {
	if deps.Rooms == nil || deps.Counts == nil || deps.Timers == nil || deps.Events == nil {
		return nil, fmt.Errorf("%w: member count repair needs rooms, member counts, timers and events", apperr.ErrInvalidArgument)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &MemberCountRepair{deps: deps}, nil
}

func (r *MemberCountRepair) Repaired() uint64 { return r.repaired.Load() }

func (r *MemberCountRepair) Repair(ctx context.Context, c Check) error {
	room, err := r.deps.Rooms.Get(ctx, c.Room)
	if err != nil {
		return err
	}
	n, err := r.deps.Counts.CountMembers(ctx, c.Room)
	switch {
	case err != nil:
		return err
	case n == room.MemberCount && room.MemberCountVer >= 1:
		return r.announce(room, domain.MemberCount{Count: room.MemberCount, Ver: room.MemberCountVer})
	case n == room.MemberCount:
		return nil
	}
	if _, err := r.deps.Timers.ArmMemberCountCheck(ctx, c.Room); err != nil {
		return err
	}
	count, ok, err := r.deps.Counts.SetMemberCount(ctx, c.Room, room.MemberCountVer, n)
	switch {
	case err != nil:
		return err
	case !ok:
		return errMemberCountMoved
	}
	r.repaired.Add(1)
	return r.announce(room, count)
}

func (r *MemberCountRepair) announce(room domain.Room, count domain.MemberCount) error {
	ev := pbconv.MemberCountChanged(room, count, "", stamp(r.deps.Now))
	return r.deps.Events.Enqueue(room.ID, []*chatimv1.Event{ev})
}
