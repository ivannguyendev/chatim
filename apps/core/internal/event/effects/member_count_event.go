package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

const MemberCountEventName = "member_count_event"

type MemberCountEvent struct{ memberEffect }

func NewMemberCountEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*MemberCountEvent, error) {
	e := &MemberCountEvent{}
	if err := e.setup(MemberCountEventName, deps, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *MemberCountEvent) Effect() Effect {
	return Effect{Name: MemberCountEventName, Delay: e.delay, Run: e.run}
}

func (e *MemberCountEvent) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	groups := groupRecords(recs, recordRoom)
	var pending []pendingAck
	for _, g := range groups {
		r, err := e.rooms.Get(ctx, g.key)
		switch {
		case undeliverable(err):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		case r.MemberCountVer >= 1:
			c := domain.MemberCount{Count: r.MemberCount, Ver: r.MemberCountVer}
			pending = e.queue(pending, errs, g.indexes[0], g.key, pbconv.MemberCountChanged(r, c, "", e.stamp()))
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&e.republished))
	for _, g := range groups {
		g.share(errs)
	}
	return errs
}
