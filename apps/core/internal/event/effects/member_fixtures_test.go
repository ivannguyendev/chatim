package effects_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var memberNow = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

type memberRig struct {
	rooms   *memstore.Rooms
	hidden  *memstore.Hidden
	js      *publishtest.JetStream
	room    domain.Room
	member  *effects.MemberEvent
	count   *effects.MemberCountEvent
	read    *effects.ReadEvent
	hide    *effects.HiddenEvent
	cleared *effects.HistoryClearedEvent
}

func newMemberRig(t *testing.T) *memberRig {
	t.Helper()
	rg := &memberRig{rooms: memstore.NewRooms(), hidden: memstore.NewHidden(), js: &publishtest.JetStream{}}
	rg.room = createRoom(t, rg.rooms, room)
	now := func() time.Time { return memberNow }
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16}
	members := effects.MemberEventDeps{Members: rg.rooms, Rooms: rg.rooms, JS: rg.js, Now: now}
	rg.member = built(effects.NewMemberEvent(members, events))(t)
	rg.count = built(effects.NewMemberCountEvent(members, events))(t)
	rg.read = built(effects.NewReadEvent(members, events))(t)
	rg.cleared = built(effects.NewHistoryClearedEvent(members, events))(t)
	rg.hide = built(effects.NewHiddenEvent(effects.HiddenEventDeps{Hidden: rg.hidden, Rooms: rg.rooms, JS: rg.js}, events))(t)
	return rg
}

func built[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}

func (rg *memberRig) join(t *testing.T, users ...string) []domain.Member {
	t.Helper()
	j := domain.Join{Room: room, Tenant: tenant, RequestID: "req-1", By: "alice", At: memberNow}
	res, err := rg.rooms.AddMembers(t.Context(), j, users)
	if err != nil {
		t.Fatalf("AddMembers(%v): %v", users, err)
	}
	return res.Members
}

func (rg *memberRig) doc(t *testing.T, user string) domain.Member {
	t.Helper()
	found, err := rg.rooms.MembersOf(t.Context(), room, []string{user})
	if err != nil || len(found) != 1 {
		t.Fatalf("MembersOf(%s) = %v, %v", user, found, err)
	}
	return found[0]
}

func (rg *memberRig) change(t *testing.T, user string, role domain.Role, state domain.MemberState) domain.Member {
	t.Helper()
	cur := rg.doc(t, user)
	next := cur.Next(role, state, cur.Priority, "req-2", "alice", memberNow.Add(time.Second))
	if ok, err := rg.rooms.ApplyMember(t.Context(), cur, next); err != nil || !ok {
		t.Fatalf("ApplyMember(%s) = %v, %v", user, ok, err)
	}
	return rg.doc(t, user)
}

func (rg *memberRig) stored(t *testing.T) domain.Room {
	t.Helper()
	r, err := rg.rooms.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get room: %v", err)
	}
	return r
}

func memberRec(r uint64, user string, ver uint32) work.Record {
	return work.Record{Kind: store.MemberChanged, Room: r, User: user, Version: ver, CommittedAt: time.Now()}
}

func readRec(r uint64, user string, ver uint32) work.Record {
	return work.Record{Kind: store.ReadChanged, Room: r, User: user, Version: ver, CommittedAt: time.Now()}
}

func hiddenRec(r uint64, user string, thread, seq uint64) work.Record {
	return work.Record{Kind: store.MessageHidden, Room: r, User: user, Thread: thread, Seq: seq, CommittedAt: time.Now()}
}

func clearedRec(r uint64, user string) work.Record {
	return work.Record{Kind: store.HistoryCleared, Room: r, User: user, CommittedAt: time.Now()}
}

type brokenMembers struct{}

func (brokenMembers) MembersOf(context.Context, uint64, []string) ([]domain.Member, error) {
	return nil, errBoom
}

func (brokenMembers) Get(context.Context, string, store.MsgKey) (domain.HiddenMessage, bool, error) {
	return domain.HiddenMessage{}, false, errBoom
}
