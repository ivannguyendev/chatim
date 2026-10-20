package room_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const tenant = "acme"

var (
	created = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
)

type recordingEvents struct {
	mu     sync.Mutex
	rooms  []uint64
	events []*chatimv1.Event
}

func (r *recordingEvents) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.rooms = append(r.rooms, room)
		r.events = append(r.events, ev)
	}
	return nil
}

func (r *recordingEvents) list() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

type newRequests struct{}

func (newRequests) Begin(context.Context, dedupe.Key) (dedupe.RequestStatus, dedupe.Record, error) {
	return dedupe.RequestNew, dedupe.Record{}, nil
}

func (newRequests) Finish(context.Context, dedupe.Key, dedupe.Record) {}

func (newRequests) Cancel(context.Context, dedupe.Key) {}

type nopTimers struct{}

func (nopTimers) ArmMemberCountCheck(context.Context, uint64) (timer.Timer, error) {
	return timer.Timer{Seq: 1}, nil
}

func (nopTimers) Disarm(context.Context, timer.Timer) {}

type nopForgetter struct{}

func (nopForgetter) ForgetMembers(uint64) {}

type rig struct {
	m      *room.Service
	msgs   *memstore.Messages
	rooms  *memstore.Rooms
	events *recordingEvents
	now    time.Time
}

func newRig(t *testing.T, policy access.Policy) *rig {
	t.Helper()
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), events: &recordingEvents{},
		now: created.Add(time.Minute + 1500*time.Microsecond),
	}
	rg.m = rg.build(t, rg.deps(t, policy))
	return rg
}

func (rg *rig) deps(t *testing.T, policy access.Policy) room.Deps {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	clock := func() time.Time { return rg.now }
	members, err := member.New(member.Deps{
		Access: checker, Messages: rg.msgs, Rooms: rg.rooms, Events: rg.events, Now: clock,
		Members: rg.rooms, Requests: newRequests{}, Forget: nopForgetter{}, Timers: nopTimers{}, Reads: rg.rooms,
	})
	if err != nil {
		t.Fatalf("member.New: %v", err)
	}
	return room.Deps{
		Access: checker, Rooms: rg.rooms, Directs: memstore.NewDirectRooms(), Members: members, Requests: newRequests{}, Events: rg.events, Now: clock,
	}
}

func (rg *rig) build(t *testing.T, d room.Deps) *room.Service {
	t.Helper()
	s, err := room.New(d)
	if err != nil {
		t.Fatalf("room.New: %v", err)
	}
	return s
}

func (rg *rig) at() time.Time { return rg.now.Truncate(time.Millisecond) }
