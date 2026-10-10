package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type scriptedProjector struct {
	mutate.PinProjector
	stale   int
	current int
	project error
}

func (p *scriptedProjector) Current(ctx context.Context, r uint64) (domain.PinState, error) {
	p.current++
	if p.current <= p.stale {
		return domain.PinState{}, nil
	}
	return p.PinProjector.Current(ctx, r)
}

func (p *scriptedProjector) Project(ctx context.Context, r, target uint64) (domain.PinState, error) {
	if p.project != nil {
		return domain.PinState{}, p.project
	}
	return p.PinProjector.Project(ctx, r, target)
}

func (rg *rig) withProjector(t *testing.T, p *scriptedProjector) *scriptedProjector {
	t.Helper()
	d := rg.deps(t, nil)
	p.PinProjector = d.Projector
	d.Projector = p
	rg.m = rg.build(t, d)
	return p
}

func (rg *rig) appendPin(t *testing.T, a domain.PinAction) domain.PinAction {
	t.Helper()
	if err := rg.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("append pin v%d: %v", a.PV, err)
	}
	return a
}

func TestADuplicatePinVersionFromTheSameCommandIsItsResult(t *testing.T) {
	rg := newRig(t, nil)
	p := rg.withProjector(t, &scriptedProjector{stale: 1})
	rg.send(t, 1, "alice", "hi")
	earlier := rg.appendPin(t, domain.PinAction{Room: room, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 1, By: "bob", At: created})
	got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
	if err != nil || got.Version != 1 || !slices.Equal(pinnedSeqs(got), []uint64{1}) {
		t.Fatalf("Pin = %+v, %v; want the stored fact as the result", got, err)
	}
	if facts := rg.pinFacts(t); len(facts) != 1 || p.current != 1 {
		t.Fatalf("facts %+v after %d reads, want the one stored fact after one read", facts, p.current)
	}
	_, events := rg.events.list()
	if len(events) != 1 || !proto.Equal(events[0], pbconv.MessagePinned(domain.RoomGroup, rg.stored(t, 1), earlier)) {
		t.Fatalf("events = %v, want msg_pinned built from the stored fact", events)
	}
}

func TestAnotherFactAtThePinVersionReturnsRetryLaterAtOnce(t *testing.T) {
	rg := newRig(t, nil)
	p := rg.withProjector(t, &scriptedProjector{stale: 1})
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.appendPin(t, domain.PinAction{Room: room, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 2, By: "carol", At: created})
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 1)); !errors.Is(err, domain.ErrRetryLater) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Pin = %v, want ErrRetryLater on the first lost append", err)
	}
	if facts := rg.pinFacts(t); len(facts) != 1 || p.current != 1 {
		t.Fatalf("facts %+v after %d reads, want only carol's fact after one read", facts, p.current)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("a lost pin enqueued %v", events)
	}
	got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
	if err != nil || got.Version != 2 || !slices.Equal(pinnedSeqs(got), []uint64{1, 2}) || p.current != 2 {
		t.Fatalf("Pin called again = %+v, %v after %d reads; want version 2 with seq 1 first after one more read", got, err, p.current)
	}
}

func TestAFailedProjectionFallsBackToTheFold(t *testing.T) {
	for _, cause := range []error{pinproj.ErrContended, errBoom} {
		rg := newRig(t, nil)
		rg.withProjector(t, &scriptedProjector{project: cause})
		rg.events.err = errBoom
		rg.send(t, 1, "alice", "hi")
		got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
		if err != nil || got.Version != 1 || !slices.Equal(pinnedSeqs(got), []uint64{1}) {
			t.Fatalf("Pin with projection error %v = %+v, %v; want the folded state", cause, got, err)
		}
		if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || stored.Version != 0 {
			t.Fatalf("projection = %+v, %v; want it left to the worker", stored, err)
		}
	}
}

func TestALostProjectionCASStillAnswersTheFoldAndTheNextProjectHeals(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	d := rg.deps(t, nil)
	rooms := &losingRooms{PinProjector: rg.rooms}
	projector, err := pinproj.New(rg.pins, rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	d.Projector = projector
	rg.m = rg.build(t, d)
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 2)); err != nil {
		t.Fatalf("first Pin: %v", err)
	}
	rooms.lose = 1
	got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
	if err != nil || got.Version != 2 || !slices.Equal(pinnedSeqs(got), []uint64{1, 2}) {
		t.Fatalf("Pin after a lost CAS = %+v, %v; want version 2 with both pins", got, err)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || stored.Version != 1 {
		t.Fatalf("projection after a lost CAS = %+v, %v; want it still at v1", stored, err)
	}
	healed, err := projector.Project(t.Context(), room, 2)
	if err != nil || !slices.Equal(pinnedSeqs(healed), []uint64{1, 2}) {
		t.Fatalf("worker Project = %+v, %v; want both pins", healed, err)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || stored.Version != 2 {
		t.Fatalf("projection after the worker = %+v, %v; want v2", stored, err)
	}
}

type losingRooms struct {
	store.PinProjector
	lose int
}

func (l *losingRooms) ApplyPins(ctx context.Context, r, base uint64, s domain.PinState) (bool, error) {
	if l.lose > 0 {
		l.lose--
		return false, nil
	}
	return l.PinProjector.ApplyPins(ctx, r, base, s)
}

func TestPinAsksThePolicyAndChecksTheTarget(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 1)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Pin = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.Unpin(t.Context(), pinCmd("bob", 1)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Unpin = %v, want PermissionDenied", err)
	}
	if len(asked) != 2 || asked[0].Action != access.PinMessage || asked[1].Action != access.UnpinMessage || asked[0].Author != "alice" || asked[0].Kind != domain.KindText {
		t.Fatalf("policy asked %+v, want pin_message then unpin_message on alice's text", asked)
	}
	open := newRig(t, access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}})
	open.send(t, 1, "alice", "hi")
	threaded, elsewhere := pinCmd("bob", 1), pinCmd("bob", 1)
	threaded.Thread, elsewhere.Room = 1, 999
	cases := []struct {
		name string
		cmd  mutate.PinCmd
		want error
	}{
		{"stranger", pinCmd("mallory", 1), domain.ErrNotMember},
		{"unknown message", pinCmd("bob", 9), domain.ErrMessageNotFound},
		{"thread", threaded, apperr.ErrInvalidArgument},
		{"unknown room", elsewhere, domain.ErrRoomNotFound},
	}
	for _, c := range cases {
		if _, err := open.m.Pin(t.Context(), c.cmd); !errors.Is(err, c.want) {
			t.Fatalf("%s: Pin = %v, want %v", c.name, err, c.want)
		}
	}
	if facts := open.pinFacts(t); len(facts) != 0 {
		t.Fatalf("refused pins wrote facts %+v", facts)
	}
	if got, err := open.m.Pin(t.Context(), pinCmd("bob", 1)); err != nil || got.Version != 1 {
		t.Fatalf("Pin on a locked kind = %+v, %v; want it allowed (D94)", got, err)
	}
}
