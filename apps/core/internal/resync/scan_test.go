package resync_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
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
	mu  sync.Mutex
	ids []string
	err error
}

func (p *publishSpy) PublishMsg(_ context.Context, m *nats.Msg, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, m.Header.Get(jetstream.MsgIDHeader))
	return &jetstream.PubAck{}, nil
}

func (p *publishSpy) published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ids)
}

type world struct {
	rooms     *memstore.Rooms
	msgs      *memstore.Messages
	edits     *memstore.Edits
	reactions *memstore.Reactions
	pins      *memstore.Pins
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(),
		reactions: memstore.NewReactions(), pins: memstore.NewPins(),
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
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Reactions: w.reactions, Pins: w.pins, Pub: pub}
}

func TestResyncPublishesRecordsOfTheLostRangeOnly(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var want []string
	for seq := uint64(91); seq >= 31; seq-- {
		want = append(want, work.Record{Kind: store.MessageInserted, Room: busyRoom, Seq: seq}.ID())
	}
	want = append(want, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID())
	if got := pub.published(); !slices.Equal(got, want) {
		t.Fatalf("published %d ids %v,\nwant %d ids %v", len(got), got, len(want), want)
	}
	if rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61}) {
		t.Fatalf("report = %+v, want 2 rooms, 1 room record, 61 message records", rep)
	}
}

func TestResyncOfOneRoomSkipsTheActivityIndex(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MessageRecords: 5}) || len(pub.published()) != 5 {
		t.Fatalf("Run = %+v, %v with %d published; want 1 room and 5 message records", rep, err, len(pub.published()))
	}
	opts.Tenant = "other"
	if _, err := resync.Run(t.Context(), w.deps(pub), target, opts); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Run(room of another tenant) = %v, want ErrInvalidArgument", err)
	}
}

func TestResyncDryRunCountsWithoutPublishingOrPacing(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: 1, DryRun: true})
	if err != nil || rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, DryRun: true}) || len(pub.published()) != 0 {
		t.Fatalf("dry run = %+v, %v with %d published; want counts only", rep, err, len(pub.published()))
	}
}

func TestResyncIsPacedByRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		pub := &publishSpy{}
		start := time.Now()
		opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: 10}
		rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
		if err != nil || rep.MessageRecords != 5 {
			t.Fatalf("Run = %+v, %v; want 5 message records", rep, err)
		}
		if took := time.Since(start); took != 500*time.Millisecond {
			t.Fatalf("5 records at 10/s took %v, want 500ms", took)
		}
	})
}

func TestResyncStopsAtTheFirstPublishError(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{err: errPublish}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if !errors.Is(err, errPublish) || rep.MessageRecords != 0 || rep.RoomRecords != 0 {
		t.Fatalf("Run = %+v, %v; want errPublish and nothing counted", rep, err)
	}
}
