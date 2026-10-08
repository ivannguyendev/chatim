package pinproj_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const room uint64 = 42

var at = time.UnixMilli(1_700_000_000_000).UTC()

func newRooms(t *testing.T) *memstore.Rooms {
	t.Helper()
	rooms := memstore.NewRooms()
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: at, MemberCount: 1}
	owner := domain.Member{Room: room, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: at}
	if err := rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rooms
}

func fact(pv uint64, op domain.PinOp, seq uint64) domain.PinAction {
	return domain.PinAction{Room: room, PV: pv, Tenant: "acme", Op: op, Seq: seq, By: "alice", At: at}
}

func pinned(pv, seq uint64) domain.Pin { return domain.Pin{Seq: seq, By: "alice", At: at, PV: pv} }

func appendFacts(t *testing.T, pins *memstore.Pins, facts ...domain.PinAction) {
	t.Helper()
	for _, a := range facts {
		if err := pins.Append(t.Context(), a); err != nil {
			t.Fatalf("Append(v%d): %v", a.PV, err)
		}
	}
}

func same(a, b domain.PinState) bool { return a.Version == b.Version && slices.Equal(a.Pins, b.Pins) }

func projector(t *testing.T, facts pinproj.Facts, rooms store.PinProjector) *pinproj.Projector {
	t.Helper()
	p, err := pinproj.New(facts, rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	return p
}

func storedState(t *testing.T, rooms store.PinProjector) domain.PinState {
	t.Helper()
	s, err := rooms.PinState(t.Context(), room)
	if err != nil {
		t.Fatalf("PinState: %v", err)
	}
	return s
}

type countingRooms struct {
	*memstore.Rooms
	applies, lose int
}

func (c *countingRooms) ApplyPins(ctx context.Context, r, base uint64, s domain.PinState) (bool, error) {
	c.applies++
	if c.lose > 0 {
		c.lose--
		return false, nil
	}
	return c.Rooms.ApplyPins(ctx, r, base, s)
}

type rival struct {
	*memstore.Rooms
	raced bool
}

func (r *rival) ApplyPins(ctx context.Context, id, base uint64, s domain.PinState) (bool, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Rooms.ApplyPins(ctx, id, base, s); err != nil {
			return false, err
		}
		return false, nil
	}
	return r.Rooms.ApplyPins(ctx, id, base, s)
}
