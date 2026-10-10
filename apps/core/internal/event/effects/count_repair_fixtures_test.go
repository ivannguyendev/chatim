package effects_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var repairedAt = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

type armedCheck struct {
	key     store.MsgKey
	counter string
}

type fakeMessageTimers struct {
	mu    sync.Mutex
	armed []armedCheck
	err   error
	onArm func()
}

func (f *fakeMessageTimers) ArmMessageCountCheck(_ context.Context, key store.MsgKey, counter string) (work.Timer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return work.Timer{}, f.err
	}
	f.armed = append(f.armed, armedCheck{key: key, counter: counter})
	if f.onArm != nil {
		f.onArm()
	}
	return work.Timer{Seq: uint64(len(f.armed))}, nil
}

func (f *fakeMessageTimers) checks() []armedCheck {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]armedCheck(nil), f.armed...)
}

type countRig struct {
	msgs   *memstore.Messages
	rooms  *memstore.Rooms
	inter  *memstore.Interactions
	js     *publishtest.JetStream
	timers *fakeMessageTimers
	repair *effects.CountRepair
}

func newCountRig(t *testing.T) *countRig {
	t.Helper()
	rg := &countRig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), inter: memstore.NewInteractions(), js: &publishtest.JetStream{}, timers: &fakeMessageTimers{}}
	createRoom(t, rg.rooms, room)
	rg.repair = rg.build(t, rg.deps())
	for seq := uint64(1); seq <= 4; seq++ {
		m := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: repairedAt}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert %d: %+v", seq, res)
		}
	}
	return rg
}

func (rg *countRig) deps() effects.CountRepairDeps {
	return effects.CountRepairDeps{
		Messages: rg.msgs, Interactions: rg.inter, Counts: rg.msgs, Timers: rg.timers, Rooms: rg.rooms, JS: rg.js,
		Now: func() time.Time { return repairedAt },
	}
}

func (rg *countRig) build(t *testing.T, d effects.CountRepairDeps) *effects.CountRepair {
	t.Helper()
	return built(effects.NewCountRepair(d, effects.CountRepairConfig{SubjectRoot: "evt", RoomCache: 16}))(t)
}

func (rg *countRig) react(t *testing.T, seq uint64, user, emoji string) {
	t.Helper()
	r := domain.Reaction{Room: room, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: repairedAt}
	if _, _, err := rg.inter.SetReaction(t.Context(), r); err != nil {
		t.Fatalf("SetReaction: %v", err)
	}
}

func (rg *countRig) reply(t *testing.T, parent, seq uint64) {
	t.Helper()
	r := domain.Reply{Parent: domain.MsgKey{Room: room, Seq: parent}, Room: room, Seq: seq, Tenant: tenant, From: "bob", At: repairedAt}
	if _, err := rg.inter.AddReply(t.Context(), r); err != nil {
		t.Fatalf("AddReply: %v", err)
	}
}

func (rg *countRig) addReactions(t *testing.T, seq uint64, deltas ...store.EmojiDelta) {
	t.Helper()
	if _, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: room, Seq: seq}, deltas); err != nil {
		t.Fatalf("AddReactionCounts: %v", err)
	}
}

func (rg *countRig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("Find %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func (rg *countRig) run(t *testing.T, recs ...work.Record) []error {
	t.Helper()
	return rg.repair.Effect().Run(t.Context(), recs)
}

func countRec(r, seq uint64, counter string, op uint32) work.Record {
	return work.Record{Kind: store.MessageCountCheck, Room: r, Seq: seq, Version: op, User: counter, CommittedAt: time.Now()}
}

type movingInteractions struct {
	effects.MessageCountReader
	move func()
}

func (m movingInteractions) CountReactions(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error) {
	counts, err := m.MessageCountReader.CountReactions(ctx, key)
	m.move()
	return counts, err
}

type orderedCounts struct {
	effects.MessageCountWriter
	calls *[]string
}

func (o orderedCounts) SetReplyCount(ctx context.Context, key store.MsgKey, base uint64, n uint32) (bool, error) {
	*o.calls = append(*o.calls, "set")
	return o.MessageCountWriter.SetReplyCount(ctx, key, base, n)
}

type brokenCounts struct{}

func (brokenCounts) CountReactions(context.Context, store.MsgKey) ([]domain.ReactionCount, error) {
	return nil, errBoom
}

func (brokenCounts) CountLiveReplies(context.Context, store.MsgKey) (uint32, error) {
	return 0, errBoom
}
