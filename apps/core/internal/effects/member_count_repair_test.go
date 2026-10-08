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

func TestACorrectCountArmsNothing(t *testing.T) {
	rg := newMemberRig(t)
	if e := rg.repair.Effect(); e.Name != effects.MemberCountRepairName || e.Delay != 0 {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.MemberCountRepairName)
	}
	before := rg.stored(t)
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if after := rg.stored(t); after.MemberCountVer != before.MemberCountVer || rg.repair.Repaired() != 0 || len(rg.timers.rooms()) != 0 {
		t.Fatalf("ver %d -> %d, repaired %d, armed %v; want the count untouched", before.MemberCountVer, after.MemberCountVer, rg.repair.Repaired(), rg.timers.rooms())
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberCountEventID(room, before.MemberCountVer)}) {
		t.Fatalf("stored = %v, want the current count announced again", got)
	}
}

func TestRepairRepublishesACorrectCountWhoseEventWasLost(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	rg.js.RefuseWhen(func(*nats.Msg) error { return errBoom })
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want a retry when the announcement fails", errs)
	}
	if r := rg.stored(t); r.MemberCount != 2 || r.MemberCountVer != 2 || len(rg.js.Stored()) != 0 {
		t.Fatalf("count %d ver %d stored %d; want the repair written and its event lost", r.MemberCount, r.MemberCountVer, len(rg.js.Stored()))
	}
	rg.js.RefuseWhen(nil)
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberCountEventID(room, 2)}) || rg.repair.Repaired() != 1 || rg.repair.Republished() != 1 {
		t.Fatalf("stored %v, repaired %d, republished %d; want the lost event sent on the retry without another repair", got, rg.repair.Repaired(), rg.repair.Republished())
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
	if armed := rg.timers.rooms(); !slices.Equal(armed, []uint64{room}) {
		t.Fatalf("armed %v, want one follow-up timer for the room", armed)
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
	if armed := rg.timers.rooms(); !slices.Equal(armed, []uint64{room}) {
		t.Fatalf("armed %v, want the follow-up timer left armed", armed)
	}
}

type orderedCounter struct {
	effects.MemberCounter
	calls *[]string
}

func (o orderedCounter) SetMemberCount(ctx context.Context, r, base uint64, n int) (domain.MemberCount, bool, error) {
	*o.calls = append(*o.calls, "set")
	return o.MemberCounter.SetMemberCount(ctx, r, base, n)
}

func TestRepairArmsAFollowUpTimerBeforeItWrites(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	var calls []string
	rg.timers.onArm = func() { calls = append(calls, "arm") }
	rg.repair = built(effects.NewMemberCountRepair(effects.MemberCountRepairDeps{
		Rooms: rg.rooms, Timers: rg.timers, JS: rg.js, Counts: orderedCounter{MemberCounter: rg.rooms, calls: &calls},
	}, effects.MemberCountRepairConfig{SubjectRoot: "evt"}))(t)
	if errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if !slices.Equal(calls, []string{"arm", "set"}) || !slices.Equal(rg.timers.rooms(), []uint64{room}) || rg.repair.Repaired() != 1 {
		t.Fatalf("calls %v, armed %v, repaired %d; want the follow-up timer armed before the write", calls, rg.timers.rooms(), rg.repair.Repaired())
	}
}

func TestAFailedArmWritesNothing(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	rg.timers.err = errBoom
	before := rg.stored(t)
	errs := rg.repair.Effect().Run(t.Context(), []work.Record{countCheck(room, 7)})
	if len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want a retry when the follow-up timer is not armed", errs)
	}
	if after := rg.stored(t); after != before || len(rg.js.Attempts()) != 0 || rg.repair.Repaired() != 0 {
		t.Fatalf("room %+v -> %+v, attempts %d, repaired %d; want nothing written or sent", before, after, len(rg.js.Attempts()), rg.repair.Repaired())
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
