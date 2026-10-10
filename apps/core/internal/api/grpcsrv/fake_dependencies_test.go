package grpcsrv_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type acceptAllCIDs struct{}

func (acceptAllCIDs) Reserve(_ context.Context, keys []dedupe.Key) ([]dedupe.Verdict, error) {
	out := make([]dedupe.Verdict, len(keys))
	for i := range out {
		out[i] = dedupe.Verdict{Status: dedupe.Reserved}
	}
	return out, nil
}

func (acceptAllCIDs) Commit(context.Context, []dedupe.Entry) error { return nil }

func (acceptAllCIDs) Abort(context.Context, []dedupe.Key) error { return nil }

type nopTimers struct{}

func (nopTimers) ArmMemberCountCheck(context.Context, uint64) (work.Timer, error) {
	return work.Timer{Seq: 1}, nil
}

func (nopTimers) ArmMessageCountCheck(context.Context, store.MsgKey, string) (work.Timer, error) {
	return work.Timer{}, nil
}

func (nopTimers) Disarm(context.Context, work.Timer) {}

type nopForgetter struct{}

func (nopForgetter) ForgetMembers(uint64) {}

type nopPublisher struct{}

func (nopPublisher) Enqueue(uint64, []*chatimv1.Event) error { return nil }

type fakeSender struct {
	mu   sync.Mutex
	cmds []actor.SendCmd
	ack  actor.Ack
	err  error
}

func (f *fakeSender) Send(_ context.Context, c actor.SendCmd) (actor.Ack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, c)
	return f.ack, f.err
}

func (f *fakeSender) sent() []actor.SendCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]actor.SendCmd(nil), f.cmds...)
}

func memStores() *rig {
	return &rig{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(), hidden: memstore.NewHidden(),
		reactions: memstore.NewInteractions(), pins: memstore.NewPins(),
	}
}

func newMutator(t *testing.T, rg *rig, o options) *mutate.Mutator {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	projector, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	var events mutate.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	var forget mutate.MemberForgetter = nopForgetter{}
	if f, ok := o.sender.(mutate.MemberForgetter); ok {
		forget = f
	}
	requests, err := dedupe.NewRequests(acceptAllCIDs{}, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewRequests: %v", err)
	}
	m, err := mutate.New(mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: events, Now: o.now,
		Interactions: rg.reactions, Counts: rg.msgs, CountTimers: nopTimers{}, Pins: rg.pins, Projector: projector, Limits: o.limits,
		Members: rg.rooms, Requests: requests, Forget: forget, Timers: nopTimers{}, Reads: rg.rooms,
	})
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
}
