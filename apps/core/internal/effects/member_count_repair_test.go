package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type movingCounter struct {
	effects.MemberCounter
	move func()
}

func (m movingCounter) CountMembers(ctx context.Context, r uint64) (int, error) {
	n, err := m.MemberCounter.CountMembers(ctx, r)
	m.move()
	return n, err
}

func TestRepairLeavesACorrectCountAlone(t *testing.T) {
	rg := newMemberRig(t)
	if e := rg.repair.Effect(); e.Name != effects.MemberCountRepairName || e.Delay != 0 {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.MemberCountRepairName)
	}
	before := rg.stored(t)
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if after := rg.stored(t); after.MemberCountVer != before.MemberCountVer || len(rg.js.Attempts()) != 0 || rg.repair.Repaired() != 0 || len(rg.timers.rooms()) != 0 {
		t.Fatalf("ver %d -> %d, attempts %d, repaired %d, armed %v; want nothing touched", before.MemberCountVer, after.MemberCountVer, len(rg.js.Attempts()), rg.repair.Repaired(), rg.timers.rooms())
	}
}

func TestRepairFixesADriftedCountAndAnnouncesIt(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob", "carol")
	recs := []work.Record{countCheck(room, 7), countCheck(room, 8)}
	if errs := rg.repair.Effect().Run(t.Context(), recs); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	r := rg.stored(t)
	if r.MemberCount != 3 || r.MemberCountVer != 2 || rg.repair.Repaired() != 1 {
		t.Fatalf("count %d ver %d repaired %d; want one repair to 3 at ver 2", r.MemberCount, r.MemberCountVer, rg.repair.Repaired())
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberCountEventID(room, 2)}) || rg.repair.Republished() != 1 {
		t.Fatalf("stored %v, republished %d; want the repaired count announced once", got, rg.repair.Republished())
	}
	events, err := rg.js.Events()
	if want := pbconv.MemberCountChanged(r, domain.MemberCount{Count: 3, Ver: 2}, "", memberNow); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if armed := rg.timers.rooms(); len(armed) != 0 {
		t.Fatalf("armed %v, want no new timer when nothing slipped in", armed)
	}
}

func TestRepairNaksWhenTheCountMovedDuringIt(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	rg.repair = built(effects.NewMemberCountRepair(effects.MemberCountRepairDeps{
		Rooms: rg.rooms, Timers: rg.timers, JS: rg.js,
		Counts: movingCounter{MemberCounter: rg.rooms, move: func() { rg.bump(t, 1) }},
	}, effects.MemberCountRepairConfig{SubjectRoot: "evt"}))(t)
	errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)})
	if len(errs) != 1 || !errors.Is(errs[0], domain.ErrRetryLater) {
		t.Fatalf("errs = %v, want a retry after the CAS missed", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.repair.Repaired() != 0 || rg.repair.Dropped() != 0 {
		t.Fatalf("attempts %d, repaired %d, dropped %d; want nothing", len(rg.js.Attempts()), rg.repair.Repaired(), rg.repair.Dropped())
	}
}

func TestRepairArmsAnotherTimerWhenAnIncrementSlippedIn(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	rg.js.RefuseWhen(func(*nats.Msg) error { rg.bump(t, 1); return nil })
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if armed := rg.timers.rooms(); !slices.Equal(armed, []uint64{room}) || rg.repair.Repaired() != 1 {
		t.Fatalf("armed %v, repaired %d; want one new timer for the room", armed, rg.repair.Repaired())
	}
	rg.timers.err = errBoom
	rg.js.RefuseWhen(func(*nats.Msg) error { rg.bump(t, -1); return nil })
	rg.bump(t, 5)
	errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 8)})
	if len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want a retry when the new timer is not armed", errs)
	}
}

func TestRepairDropsAGoneRoom(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(otherRoom, 7)}); !allNil(errs, 1) || rg.repair.Dropped() != 1 {
		t.Fatalf("errs %v, dropped %d; want the record dropped", errs, rg.repair.Dropped())
	}
	eff := built(effects.NewMemberCountRepair(
		effects.MemberCountRepairDeps{Rooms: rg.rooms, Counts: brokenMembers{}, Timers: rg.timers, JS: &publishtest.JetStream{}},
		effects.MemberCountRepairConfig{SubjectRoot: "evt"},
	))(t)
	if errs := eff.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func (rg *memberRig) bump(t *testing.T, delta int) {
	t.Helper()
	if _, err := rg.rooms.AddMemberCount(t.Context(), room, delta); err != nil {
		t.Fatalf("AddMemberCount(%d): %v", delta, err)
	}
}
