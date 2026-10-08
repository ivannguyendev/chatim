package effects_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const (
	countDelay        = time.Second
	otherRoom  uint64 = 4343
)

var countedAt = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

type reactRig struct {
	msgs      *memstore.Messages
	rooms     *memstore.Rooms
	reactions *memstore.Reactions
	pins      *memstore.Pins
	js        *publishtest.JetStream
	touches   *spyCounter
	projects  *spyProjector
	counter   *effects.ReactionCounter
	event     *effects.ReactionEvent
	pinProj   *effects.PinProjection
	pinEvent  *effects.PinEvent
}

func newReactRig(t *testing.T) *reactRig {
	t.Helper()
	rg := &reactRig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), reactions: memstore.NewReactions(), pins: memstore.NewPins(), js: &publishtest.JetStream{}}
	createRoom(t, rg.rooms, room)
	counts, err := counter.New(rg.msgs, rg.reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	proj, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	rg.touches, rg.projects = &spyCounter{inner: counts}, &spyProjector{inner: proj}
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16}
	if rg.counter, err = effects.NewReactionCounter(
		effects.ReactionCounterDeps{Messages: rg.msgs, Counter: rg.touches, Rooms: rg.rooms, JS: rg.js, Now: func() time.Time { return countedAt }},
		effects.ReactionCounterConfig{SubjectRoot: "evt", Delay: countDelay, RoomCache: 16, Tries: 2},
	); err != nil {
		t.Fatalf("NewReactionCounter: %v", err)
	}
	if rg.event, err = effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: rg.reactions, Rooms: rg.rooms, JS: rg.js}, events); err != nil {
		t.Fatalf("NewReactionEvent: %v", err)
	}
	if rg.pinProj, err = effects.NewPinProjection(rg.projects); err != nil {
		t.Fatalf("NewPinProjection: %v", err)
	}
	if rg.pinEvent, err = effects.NewPinEvent(effects.PinEventDeps{Pins: rg.pins, Messages: rg.msgs, Rooms: rg.rooms, JS: rg.js}, events); err != nil {
		t.Fatalf("NewPinEvent: %v", err)
	}
	return rg
}

func (rg *reactRig) message(t *testing.T, r, seq uint64) {
	t.Helper()
	m := domain.Message{Room: r, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d/%d: %+v", r, seq, res)
	}
}

func (rg *reactRig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("find seq %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func (rg *reactRig) react(t *testing.T, seq uint64, user, emoji string) {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Millisecond)
	var err error
	if emoji == "" {
		_, _, err = rg.reactions.Remove(t.Context(), store.MsgKey{Room: room, Seq: seq}, user, at)
	} else {
		_, _, err = rg.reactions.Set(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: at})
	}
	if err != nil {
		t.Fatalf("%s reacts %q on seq %d: %v", user, emoji, seq, err)
	}
}

func (rg *reactRig) current(t *testing.T, seq uint64, user string) domain.Reaction {
	t.Helper()
	doc, found, err := rg.reactions.Get(t.Context(), store.MsgKey{Room: room, Seq: seq}, user)
	if err != nil || !found {
		t.Fatalf("reaction of %s on seq %d = %+v, %v, %v", user, seq, doc, found, err)
	}
	return doc
}

func (rg *reactRig) pin(t *testing.T, r, pv uint64, op domain.PinOp, seq uint64) domain.PinAction {
	t.Helper()
	a := domain.PinAction{Room: r, PV: pv, Tenant: tenant, Op: op, Seq: seq, By: "bob", At: time.Now().UTC().Truncate(time.Millisecond)}
	if err := rg.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("append pin %d/%d: %v", r, pv, err)
	}
	return a
}

func reactionRec(seq uint64, user string, n uint32) work.Record {
	return work.Record{Kind: store.ReactionChanged, Room: room, Seq: seq, Version: n, User: user, CommittedAt: time.Now()}
}

func pinRec(r, pv uint64) work.Record {
	return work.Record{Kind: store.PinInserted, Room: r, Seq: pv, CommittedAt: time.Now()}
}
