package effects_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const (
	tenant        = "acme"
	room   uint64 = 4242
)

type marks struct {
	mu    sync.Mutex
	acked map[store.MsgKey]bool
	err   error
}

func (m *marks) Acked(_ context.Context, keys []store.MsgKey) ([]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := make([]bool, len(keys))
	for i, k := range keys {
		out[i] = m.acked[k]
	}
	return out, nil
}

func (m *marks) mark(keys ...store.MsgKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		m.acked[k] = true
	}
}

func (m *marks) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

type countingRooms struct {
	effects.RoomReader
	gets atomic.Int32
}

func (c *countingRooms) Get(ctx context.Context, id uint64) (domain.Room, error) {
	c.gets.Add(1)
	return c.RoomReader.Get(ctx, id)
}

type brokenStore struct{}

func (brokenStore) Find(context.Context, uint64, []store.MsgKey) ([]domain.Message, error) {
	return nil, errBoom
}

func (brokenStore) Get(context.Context, uint64) (domain.Room, error) {
	return domain.Room{}, errBoom
}

func createRoom(t *testing.T, rooms *memstore.Rooms, id uint64) domain.Room {
	t.Helper()
	created := time.Now().UTC().Truncate(time.Millisecond)
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
	got, err := rooms.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get room %d: %v", id, err)
	}
	return got
}

func storedEventIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Stored() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

type msgRig struct {
	eff   *effects.MessageCreated
	msgs  *memstore.Messages
	rooms *countingRooms
	marks *marks
	js    *publishtest.JetStream
}

func newMsgRig(t *testing.T, finder effects.MessageFinder) *msgRig {
	t.Helper()
	mem := memstore.NewRooms()
	createRoom(t, mem, room)
	rg := &msgRig{msgs: memstore.NewMessages(), rooms: &countingRooms{RoomReader: mem}, marks: &marks{acked: map[store.MsgKey]bool{}}, js: &publishtest.JetStream{}}
	if finder == nil {
		finder = rg.msgs
	}
	eff, err := effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: rg.marks, Messages: finder, Rooms: rg.rooms, JS: rg.js},
		effects.MessageCreatedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16},
	)
	if err != nil {
		t.Fatalf("NewMessageCreated: %v", err)
	}
	rg.eff = eff
	return rg
}

func (rg *msgRig) insert(t *testing.T, seqs ...uint64) []domain.Message {
	t.Helper()
	var out []domain.Message
	for _, s := range seqs {
		m := domain.Message{Room: room, Seq: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert %d: %+v", s, res)
		}
		out = append(out, m)
	}
	return out
}

func (rg *msgRig) run(ctx context.Context, recs []work.Record) []error {
	return rg.eff.Effect().Run(ctx, recs)
}

func msgRecs(r uint64, seqs ...uint64) []work.Record {
	out := make([]work.Record, len(seqs))
	for i, s := range seqs {
		out[i] = work.Record{Kind: store.MessageInserted, Room: r, Seq: s, CommittedAt: time.Now()}
	}
	return out
}

func allNil(errs []error, n int) bool {
	return len(errs) == n && !slices.ContainsFunc(errs, func(err error) bool { return err != nil })
}
