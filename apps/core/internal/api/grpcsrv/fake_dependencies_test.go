package grpcsrv_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
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

type pendingCIDs struct{ acceptAllCIDs }

func (pendingCIDs) Reserve(_ context.Context, keys []dedupe.Key) ([]dedupe.Verdict, error) {
	out := make([]dedupe.Verdict, len(keys))
	for i := range out {
		out[i] = dedupe.Verdict{Status: dedupe.PendingElsewhere}
	}
	return out, nil
}

type nopTimers struct{}

func (nopTimers) ArmMemberCountCheck(context.Context, uint64) (timer.Timer, error) {
	return timer.Timer{Seq: 1}, nil
}

func (nopTimers) ArmMessageCountCheck(context.Context, store.MsgKey, string) (timer.Timer, error) {
	return timer.Timer{}, nil
}

func (nopTimers) Disarm(context.Context, timer.Timer) {}

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
		reactions: memstore.NewInteractions(), pins: memstore.NewPins(), directs: memstore.NewDirectRooms(),
		threads: memstore.NewReplyThreads(),
	}
}

func newReader(t *testing.T, rg *rig, o options, pages message.Pages) *message.Reader {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	r, err := message.NewReader(message.ReaderDeps{Access: checker, Pages: pages, Edits: rg.edits, Hidden: rg.hidden, Interactions: rg.reactions})
	if err != nil {
		t.Fatalf("message.NewReader: %v", err)
	}
	return r
}

func newEditor(t *testing.T, rg *rig, o options) *message.Editor {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	var events message.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	m, err := message.New(message.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Events: events, Now: o.now, Interactions: rg.reactions,
	})
	if err != nil {
		t.Fatalf("message.New: %v", err)
	}
	return m
}

func newMemberService(t *testing.T, rg *rig, o options) *member.Service {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	var events member.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	var forget member.MemberForgetter = nopForgetter{}
	if f, ok := o.sender.(member.MemberForgetter); ok {
		forget = f
	}
	s, err := member.New(member.Deps{
		Access: checker, Messages: rg.msgs, Rooms: rg.rooms, Events: events, Now: o.now, Limits: o.memberLimits,
		Members: membersOf(rg, o), Requests: newRequests(t, nil), Forget: forget, Timers: nopTimers{}, Reads: rg.rooms,
	})
	if err != nil {
		t.Fatalf("member.New: %v", err)
	}
	return s
}

func newRequests(t *testing.T, reg dedupe.Registry) *dedupe.Requests {
	t.Helper()
	if reg == nil {
		reg = acceptAllCIDs{}
	}
	r, err := dedupe.NewRequests(reg, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewRequests: %v", err)
	}
	return r
}

func newRoomService(t *testing.T, rg *rig, o options, members *member.Service) *room.Service {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	var events room.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	s, err := room.New(room.Deps{
		Access: checker, Rooms: rg.rooms, Directs: rg.directs, Members: members, Requests: newRequests(t, o.pending), Events: events, NewID: o.newID, Now: o.now,
	})
	if err != nil {
		t.Fatalf("room.New: %v", err)
	}
	return s
}

func membersOf(rg *rig, o options) member.MemberStore {
	if o.members == nil {
		return rg.rooms
	}
	return o.members(rg.rooms)
}
