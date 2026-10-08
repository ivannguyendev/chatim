package effects

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MemberCountRepairName = "member_count_repair"
	MembersCounter        = "members"
)

var errCountMoved = fmt.Errorf("member count moved during the recount: %w", domain.ErrRetryLater)

type MemberCountRepairDeps struct {
	Rooms  RoomReader
	Counts MemberCounter
	Timers CountTimers
	JS     publish.JetStream
	Now    func() time.Time
}

type MemberCountRepairConfig struct {
	SubjectRoot string
}

type MemberCountRepair struct {
	eventPublisher
	deps     MemberCountRepairDeps
	repaired atomic.Uint64
}

func NewMemberCountRepair(deps MemberCountRepairDeps, cfg MemberCountRepairConfig) (*MemberCountRepair, error) {
	if deps.Rooms == nil || deps.Counts == nil || deps.Timers == nil || deps.JS == nil || cfg.SubjectRoot == "" {
		return nil, fmt.Errorf("%w: %s needs rooms, member counts, timers, a jetstream client and a subject root", apperr.ErrInvalidArgument, MemberCountRepairName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	e := &MemberCountRepair{deps: deps}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, 1)
	return e, nil
}

func (e *MemberCountRepair) Effect() Effect {
	return Effect{Name: MemberCountRepairName, Run: e.run}
}

func (e *MemberCountRepair) Repaired() uint64 { return e.repaired.Load() }

func (e *MemberCountRepair) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, recordRoom) {
		err := e.repair(ctx, g.key)
		switch {
		case undeliverable(err):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		}
	}
	return errs
}

func (e *MemberCountRepair) repair(ctx context.Context, room uint64) error {
	r, err := e.deps.Rooms.Get(ctx, room)
	if err != nil {
		return err
	}
	n, err := e.deps.Counts.CountMembers(ctx, room)
	switch {
	case err != nil:
		return err
	case n == r.MemberCount && r.MemberCountVer >= 1:
		return e.announce(ctx, r, domain.MemberCount{Count: r.MemberCount, Ver: r.MemberCountVer})
	case n == r.MemberCount:
		return nil
	}
	if _, err := e.deps.Timers.Arm(ctx, room); err != nil {
		return err
	}
	c, ok, err := e.deps.Counts.SetMemberCount(ctx, room, r.MemberCountVer, n)
	switch {
	case err != nil:
		return err
	case !ok:
		return errCountMoved
	}
	e.repaired.Add(1)
	return e.announce(ctx, r, c)
}

func (e *MemberCountRepair) announce(ctx context.Context, r domain.Room, c domain.MemberCount) error {
	errs := make([]error, 1)
	ev := pbconv.MemberCountChanged(r, c, "", e.deps.Now().UTC().Truncate(time.Millisecond))
	awaitAcks(ctx, e.queue(nil, errs, 0, r.ID, ev), errs, countStored(&e.republished))
	return errs[0]
}
