package projection_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var (
	errBoom   = errors.New("boom")
	indexedAt = time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
)

type indexRig struct {
	msgs    *memstore.Messages
	rooms   *memstore.Rooms
	inter   *memstore.Interactions
	threads *memstore.ReplyThreads
	timers  *fakeTimers
	queue   *fakeQueue
	index   *projection.ReplyIndex
}

func newIndexRig(t *testing.T) *indexRig {
	t.Helper()
	rg := &indexRig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), inter: memstore.NewInteractions(),
		threads: memstore.NewReplyThreads(), timers: &fakeTimers{}, queue: &fakeQueue{},
	}
	createRoom(t, rg.rooms, roomA, "alice", "minh")
	rg.index = rg.build(t, rg.deps())
	return rg
}

func createRoom(t *testing.T, rooms *memstore.Rooms, id uint64, users ...string) {
	t.Helper()
	members := make([]domain.Member, len(users))
	for i, u := range users {
		members[i] = domain.Member{Room: id, Tenant: tenant, User: u, Role: domain.RoleMember, JoinedAt: indexedAt}
	}
	members[0].Role = domain.RoleOwner
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: users[0], CreatedAt: indexedAt, MemberCount: len(users)}
	if err := rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
}

func (rg *indexRig) deps() projection.ReplyIndexDeps {
	return projection.ReplyIndexDeps{
		Replies: rg.inter, Threads: rg.threads, Counts: rg.msgs,
		Timers: rg.timers, Events: rg.queue, Now: func() time.Time { return indexedAt.Add(time.Hour) },
	}
}

func (rg *indexRig) build(t *testing.T, d projection.ReplyIndexDeps) *projection.ReplyIndex {
	t.Helper()
	return built(projection.NewReplyIndex(d))(t)
}

func (rg *indexRig) handle(t *testing.T, evs ...projection.Event) []error {
	t.Helper()
	return rg.index.Handle(t.Context(), evs)
}

func (rg *indexRig) send(t *testing.T, seq uint64, edit func(*domain.Message)) projection.Event {
	t.Helper()
	m := domain.Message{Room: roomA, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: indexedAt}
	if edit != nil {
		edit(&m)
	}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d: %+v", seq, res)
	}
	return decoded(t, pbconv.MessageCreated(domain.RoomGroup, m))
}

func (rg *indexRig) edit(t *testing.T, seq uint64, ver uint32, kind domain.EditKind) projection.Event {
	t.Helper()
	e := domain.Edit{Room: roomA, Seq: seq, Version: ver, Kind: kind, Tenant: tenant, By: "alice", Text: "edited", At: indexedAt.Add(time.Minute)}
	if err := rg.msgs.ApplyEdit(t.Context(), e); err != nil {
		t.Fatalf("ApplyEdit v%d: %v", ver, err)
	}
	return decoded(t, pbconv.MessageChanged(domain.RoomGroup, rg.message(t, seq), e, domain.Mentions{}))
}

func (rg *indexRig) message(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), roomA, []store.MsgKey{{Room: roomA, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("Find %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func (rg *indexRig) liveReplies(t *testing.T, parent uint64) uint32 {
	t.Helper()
	n, err := rg.inter.CountLiveReplies(t.Context(), store.MsgKey{Room: roomA, Seq: parent})
	if err != nil {
		t.Fatalf("CountLiveReplies: %v", err)
	}
	return n
}

func replyTo(parent uint64) func(*domain.Message) {
	return func(m *domain.Message) { m.ReplyTo = &domain.ReplyRef{Seq: parent} }
}

func noErrors(t *testing.T, errs []error, n int) {
	t.Helper()
	if len(errs) != n {
		t.Fatalf("errs = %v, want %d results", errs, n)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
}
