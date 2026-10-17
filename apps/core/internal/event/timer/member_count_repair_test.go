package timer_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type memberRig struct {
	rooms  *memstore.Rooms
	events *fakeEvents
	timers *fakeTimers
	repair *timer.MemberCountRepair
}

func newMemberRig(t *testing.T) *memberRig {
	t.Helper()
	rg := &memberRig{rooms: memstore.NewRooms(), events: &fakeEvents{}, timers: &fakeTimers{}}
	createRoom(t, rg.rooms, room)
	rg.repair = rg.build(t, rg.deps())
	return rg
}

func (rg *memberRig) deps() timer.MemberCountRepairDeps {
	return timer.MemberCountRepairDeps{Rooms: rg.rooms, Counts: rg.rooms, Timers: rg.timers, Events: rg.events, Now: func() time.Time { return repairedAt }}
}

func (rg *memberRig) build(t *testing.T, d timer.MemberCountRepairDeps) *timer.MemberCountRepair {
	t.Helper()
	return built(timer.NewMemberCountRepair(d))(t)
}

func (rg *memberRig) join(t *testing.T, users ...string) {
	t.Helper()
	j := domain.Join{Room: room, Tenant: tenant, RequestID: "req-1", By: "alice", At: repairedAt}
	if _, err := rg.rooms.AddMembers(t.Context(), j, users); err != nil {
		t.Fatalf("AddMembers(%v): %v", users, err)
	}
}

func (rg *memberRig) stored(t *testing.T) domain.Room {
	t.Helper()
	r, err := rg.rooms.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get room: %v", err)
	}
	return r
}

type movingCounter struct {
	timer.MemberCounter
	move func()
}

func (m movingCounter) CountMembers(ctx context.Context, r uint64) (int, error) {
	n, err := m.MemberCounter.CountMembers(ctx, r)
	m.move()
	return n, err
}

type orderedCounter struct {
	timer.MemberCounter
	calls *[]string
}

func (o orderedCounter) SetMemberCount(ctx context.Context, r, base uint64, n int) (domain.MemberCount, bool, error) {
	*o.calls = append(*o.calls, "set")
	return o.MemberCounter.SetMemberCount(ctx, r, base, n)
}

func TestACorrectMemberCountIsOnlyAnnouncedAgain(t *testing.T) {
	rg := newMemberRig(t)
	before := rg.stored(t)
	if err := rg.repair.Repair(t.Context(), memberCheck(room)); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if after := rg.stored(t); after.MemberCountVer != before.MemberCountVer || rg.repair.Repaired() != 0 || len(rg.timers.all()) != 0 {
		t.Fatalf("ver %d -> %d, repaired %d, armed %v; want the count untouched", before.MemberCountVer, after.MemberCountVer, rg.repair.Repaired(), rg.timers.all())
	}
	if got := rg.events.ids(); !slices.Equal(got, []string{pbconv.MemberCountEventID(room, before.MemberCountVer)}) {
		t.Fatalf("events = %v, want the current count announced again", got)
	}
}

func TestADriftedMemberCountIsArmedThenRewrittenAndAnnounced(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob", "carol")
	var calls []string
	rg.timers.calls = &calls
	d := rg.deps()
	d.Counts = orderedCounter{MemberCounter: rg.rooms, calls: &calls}
	rg.repair = rg.build(t, d)
	if err := rg.repair.Repair(t.Context(), memberCheck(room)); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	r := rg.stored(t)
	if r.MemberCount != 3 || r.MemberCountVer != 2 || rg.repair.Repaired() != 1 || !slices.Equal(calls, []string{"arm", "set"}) {
		t.Fatalf("count %d ver %d repaired %d calls %v; want 3 at ver 2 written after the timer", r.MemberCount, r.MemberCountVer, rg.repair.Repaired(), calls)
	}
	want := pbconv.MemberCountChanged(r, domain.MemberCount{Count: 3, Ver: 2}, "", repairedAt)
	if len(rg.events.events) != 1 || !proto.Equal(rg.events.events[0], want) || rg.events.rooms[0] != room {
		t.Fatalf("events = %v, want %v", rg.events.events, want)
	}
}

func TestAMemberCountMovedDuringTheRepairFailsWithItsTimerArmed(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	d := rg.deps()
	d.Counts = movingCounter{MemberCounter: rg.rooms, move: func() {
		if _, err := rg.rooms.AddMemberCount(t.Context(), room, 1); err != nil {
			t.Errorf("AddMemberCount: %v", err)
		}
	}}
	rg.repair = rg.build(t, d)
	if err := rg.repair.Repair(t.Context(), memberCheck(room)); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("Repair = %v, want a retry after the CAS missed", err)
	}
	if len(rg.events.ids()) != 0 || rg.repair.Repaired() != 0 || len(rg.timers.all()) != 1 {
		t.Fatalf("events %v, repaired %d, armed %v; want no event and the follow-up timer left armed", rg.events.ids(), rg.repair.Repaired(), rg.timers.all())
	}
}

func TestAFailedMemberArmWritesNothing(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	rg.timers.err = errBoom
	before := rg.stored(t)
	if err := rg.repair.Repair(t.Context(), memberCheck(room)); !errors.Is(err, errBoom) {
		t.Fatalf("Repair = %v, want the arm error", err)
	}
	if after := rg.stored(t); after != before || len(rg.events.ids()) != 0 || rg.repair.Repaired() != 0 {
		t.Fatalf("room %+v -> %+v, events %v; want nothing written or sent", before, after, rg.events.ids())
	}
}

func TestMemberRepairReportsAGoneRoomAndAFailedEnqueue(t *testing.T) {
	rg := newMemberRig(t)
	if err := rg.repair.Repair(t.Context(), memberCheck(otherRoom)); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Repair of a gone room = %v, want ErrRoomNotFound", err)
	}
	rg.events.err = errBoom
	if err := rg.repair.Repair(t.Context(), memberCheck(room)); !errors.Is(err, errBoom) {
		t.Fatalf("Repair with the publisher full = %v, want the enqueue error", err)
	}
}

func TestNewMemberCountRepairNeedsEveryDependency(t *testing.T) {
	rg := newMemberRig(t)
	strip := map[string]func(*timer.MemberCountRepairDeps){
		"rooms":  func(d *timer.MemberCountRepairDeps) { d.Rooms = nil },
		"counts": func(d *timer.MemberCountRepairDeps) { d.Counts = nil },
		"timers": func(d *timer.MemberCountRepairDeps) { d.Timers = nil },
		"events": func(d *timer.MemberCountRepairDeps) { d.Events = nil },
	}
	for name, cut := range strip {
		d := rg.deps()
		cut(&d)
		if _, err := timer.NewMemberCountRepair(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("without %s: err = %v", name, err)
		}
	}
}
