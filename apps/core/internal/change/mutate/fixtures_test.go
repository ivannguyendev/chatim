package mutate_test

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	tenant        = "acme"
	room   uint64 = 4242
)

var (
	created = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
)

type recordingEvents struct {
	mu     sync.Mutex
	rooms  []uint64
	events []*chatimv1.Event
	err    error
}

func (r *recordingEvents) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.rooms = append(r.rooms, room)
		r.events = append(r.events, ev)
	}
	return r.err
}

func (r *recordingEvents) list() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

type rig struct {
	m         *mutate.Mutator
	msgs      *memstore.Messages
	rooms     *memstore.Rooms
	edits     *memstore.Edits
	hidden    *memstore.Hidden
	reactions *memstore.Reactions
	pins      *memstore.Pins
	events    *recordingEvents
	now       time.Time
	memberParts
}

func newRig(t *testing.T, policy access.Policy) *rig {
	t.Helper()
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), hidden: memstore.NewHidden(),
		reactions: memstore.NewReactions(), pins: memstore.NewPins(), events: &recordingEvents{},
		now: created.Add(time.Minute + 1500*time.Microsecond),
	}
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 3}
	members := []domain.Member{
		{Room: room, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created},
		{Room: room, Tenant: tenant, User: "bob", Role: domain.RoleMember, JoinedAt: created},
		{Room: room, Tenant: tenant, User: "carol", Role: domain.RoleMember, JoinedAt: created},
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create room: %v", err)
	}
	rg.memberParts = newMemberParts(t, rg.rooms)
	rg.m = rg.build(t, rg.deps(t, policy))
	return rg
}

func (rg *rig) deps(t *testing.T, policy access.Policy) mutate.Deps {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	counts, err := counter.New(rg.msgs, rg.reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	projector, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	return mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: rg.events,
		Reactions: rg.reactions, Counter: counts, Pins: rg.pins, Projector: projector, Now: func() time.Time { return rg.now },
		Members: rg.members, Requests: rg.requests, Forget: rg.forgets, Timers: rg.timers, Reads: rg.rooms, NewRequestID: rg.requestIDs.next,
	}
}

func (rg *rig) build(t *testing.T, d mutate.Deps) *mutate.Mutator {
	t.Helper()
	m, err := mutate.New(d)
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
}

func (rg *rig) mutator(t *testing.T, policy access.Policy, edits store.Edits) *mutate.Mutator {
	t.Helper()
	d := rg.deps(t, policy)
	d.Edits = edits
	return rg.build(t, d)
}

func (rg *rig) send(t *testing.T, seq uint64, from, text string) domain.Message {
	t.Helper()
	m := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: from, Kind: domain.KindText, Text: text, CID: "c-" + strconv.FormatUint(seq, 10), CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
	return m
}

func (rg *rig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{key(seq)})
	if err != nil || len(found) != 1 {
		t.Fatalf("find seq %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func (rg *rig) facts(t *testing.T, seq uint64) []domain.Edit {
	t.Helper()
	got, err := rg.edits.History(t.Context(), key(seq), 0, store.MaxEditPage)
	if err != nil {
		t.Fatalf("history of seq %d: %v", seq, err)
	}
	return got
}

func (rg *rig) at() time.Time { return rg.now.Truncate(time.Millisecond) }

func key(seq uint64) store.MsgKey { return store.MsgKey{Room: room, Seq: seq} }

func edit(user string, seq uint64, base uint32, text string) mutate.EditCmd {
	return mutate.EditCmd{Tenant: tenant, User: user, Room: room, Seq: seq, BaseVersion: base, Text: text}
}

func del(user string, seq uint64, base uint32) mutate.DeleteCmd {
	return mutate.DeleteCmd{Tenant: tenant, User: user, Room: room, Seq: seq, BaseVersion: base}
}

func sameEdit(a, b domain.Edit) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}
