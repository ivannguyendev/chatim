package resync_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const (
	busyRoom    uint64 = 7_340_000_001
	newRoom     uint64 = 7_340_000_002
	staleRoom   uint64 = 7_340_000_003
	foreignRoom uint64 = 7_340_000_004
)

var (
	lostFrom   = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	lostTo     = lostFrom.Add(time.Hour)
	errPublish = errors.New("publish failed")
	target     = resync.Target{SubjectRoot: "work", Partitions: 32}
)

type publishSpy struct {
	mu   sync.Mutex
	ids  []string
	recs []work.Record
	err  error
}

func (p *publishSpy) PublishMsg(_ context.Context, m *nats.Msg, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, err := work.Decode(m.Data)
	if err != nil {
		return nil, err
	}
	p.ids = append(p.ids, m.Header.Get(jetstream.MsgIDHeader))
	p.recs = append(p.recs, rec)
	return &jetstream.PubAck{}, nil
}

func (p *publishSpy) published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ids)
}

func (p *publishSpy) records() []work.Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.recs)
}

func withoutOps(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		if strings.HasPrefix(id, "q:") {
			id = id[:strings.LastIndex(id, "-")+1]
		}
		out[i] = id
	}
	return out
}

type world struct {
	rooms     *memstore.Rooms
	msgs      *memstore.Messages
	edits     *memstore.Edits
	reactions *memstore.Interactions
	pins      *memstore.Pins
	hidden    *memstore.Hidden
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(),
		reactions: memstore.NewInteractions(), pins: memstore.NewPins(), hidden: memstore.NewHidden(),
	}
	old := lostFrom.Add(-48 * time.Hour)
	w.room(t, busyRoom, "acme", old)
	w.room(t, newRoom, "acme", lostFrom.Add(10*time.Minute))
	w.room(t, staleRoom, "acme", old)
	w.room(t, foreignRoom, "other", lostFrom.Add(20*time.Minute))
	w.messages(t, busyRoom, 150, lostFrom.Add(-30*time.Minute))
	w.messages(t, staleRoom, 5, lostFrom.Add(-3*time.Hour))
	acts := []store.Activity{
		{Room: busyRoom, Seq: 150, At: lostFrom.Add(2 * time.Hour)},
		{Room: staleRoom, Seq: 5, At: lostFrom.Add(-3 * time.Hour)},
	}
	if err := w.rooms.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	return w
}

func (w world) room(t *testing.T, id uint64, tenant string, created time.Time) {
	t.Helper()
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	owner := domain.Member{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}
	if err := w.rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create(%d): %v", id, err)
	}
}

func (w world) messages(t *testing.T, room uint64, n int, first time.Time) {
	t.Helper()
	msgs := make([]domain.Message, n)
	for i := range msgs {
		seq := uint64(i + 1)
		msgs[i] = domain.Message{
			Room: room, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi",
			CID: "c" + strconv.FormatUint(seq, 10), CreatedAt: first.Add(time.Duration(i) * time.Minute),
		}
	}
	for i, r := range w.msgs.Insert(t.Context(), msgs) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert %d/%d: %+v", room, i+1, r)
		}
	}
}

func (w world) deps(pub resync.Publisher) resync.Deps {
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Interactions: w.reactions, Pins: w.pins, Members: w.rooms, Hidden: w.hidden, Pub: pub}
}
