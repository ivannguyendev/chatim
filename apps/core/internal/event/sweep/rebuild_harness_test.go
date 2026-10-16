package sweep_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type fullRig struct {
	*rig
	edits  *memstore.Edits
	inter  *memstore.Interactions
	pins   *memstore.Pins
	hidden *memstore.Hidden
	types  *eventkit.RoomTypes
}

type stores func(f *fullRig) sweep.RebuilderStores

func newFullRig(t *testing.T, override stores) *fullRig {
	t.Helper()
	rg := &rig{
		cfg: setup, msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), marks: memstore.NewSweepCheckpoints(),
		owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}, done: make(chan error, 1),
	}
	f := &fullRig{rig: rg, edits: memstore.NewEdits(), inter: memstore.NewInteractions(), pins: memstore.NewPins(), hidden: memstore.NewHidden()}
	f.types = eventkit.NewRoomTypes(rg.rooms, 16)
	rg.states = memstore.NewEventStates(memstore.EventStateStores{
		Messages: rg.msgs, Rooms: rg.rooms, Edits: f.edits, Hidden: f.hidden, Interactions: f.inter, Pins: f.pins,
	})
	s := f.stores(t)
	if override != nil {
		s = override(f)
	}
	rbs, err := sweep.NewRebuilders(s, f.types, nil)
	if err != nil {
		t.Fatalf("NewRebuilders: %v", err)
	}
	rg.rebuilders = rbs
	rg.replaceStates(t, rg.states)
	rg.owner.leading.Store(true)
	return f
}

func (f *fullRig) stores(t *testing.T) sweep.RebuilderStores {
	t.Helper()
	proj, err := pinproj.New(f.pins, f.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	return sweep.RebuilderStores{
		Messages: f.msgs, Rooms: f.rooms, Edits: f.edits, Interactions: f.inter,
		Pins: f.pins, PinProjector: proj, Members: f.rooms, Hidden: f.hidden,
	}
}

func (f *fullRig) group(t *testing.T, at time.Time, users ...string) {
	t.Helper()
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: users[0], CreatedAt: at, MemberCount: len(users)}
	members := make([]domain.Member, len(users))
	for i, u := range users {
		role := domain.RoleMember
		if i == 0 {
			role = domain.RoleOwner
		}
		members[i] = domain.Member{Room: room, Tenant: tenant, User: u, Role: role, JoinedAt: at}
	}
	if err := f.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func (f *fullRig) settle() {
	time.Sleep(minAge + time.Second)
	f.start()
}

func (f *fullRig) published(t *testing.T, id string) *chatimv1.Event {
	t.Helper()
	evs, err := f.js.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for _, ev := range evs {
		if ev.GetId() == id {
			return ev
		}
	}
	return nil
}

func (f *fullRig) event(t *testing.T, id string) bool {
	t.Helper()
	return f.published(t, id) != nil
}

func expectEvents(t *testing.T, f *fullRig, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if !f.event(t, id) {
			t.Fatalf("event %s not published; published %v", id, f.eventIDs(t))
		}
	}
}

func clearedIn(rec *recordingStates, c store.StateCollection) []store.StateTarget {
	var out []store.StateTarget
	for _, t := range rec.clears() {
		if t.Collection == c {
			out = append(out, t)
		}
	}
	return out
}
