package timer_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type countRig struct {
	msgs   *memstore.Messages
	rooms  *memstore.Rooms
	inter  *memstore.Interactions
	events *fakeEvents
	timers *fakeTimers
	repair *timer.CountRepair
}

func newCountRig(t *testing.T) *countRig {
	t.Helper()
	rg := &countRig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), inter: memstore.NewInteractions(), events: &fakeEvents{}, timers: &fakeTimers{}}
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

func (rg *countRig) deps() timer.CountRepairDeps {
	return timer.CountRepairDeps{
		Messages: rg.msgs, Interactions: rg.inter, Counts: rg.msgs, Timers: rg.timers, Rooms: rg.rooms, Events: rg.events,
		Now: func() time.Time { return repairedAt },
	}
}

func (rg *countRig) build(t *testing.T, d timer.CountRepairDeps) *timer.CountRepair {
	t.Helper()
	return built(timer.NewCountRepair(d))(t)
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

type movingInteractions struct {
	timer.MessageCountReader
	move func()
}

func (m movingInteractions) CountReactions(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error) {
	counts, err := m.MessageCountReader.CountReactions(ctx, key)
	m.move()
	return counts, err
}

type orderedCounts struct {
	timer.MessageCountWriter
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
