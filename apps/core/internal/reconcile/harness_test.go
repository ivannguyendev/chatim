package reconcile_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant           = "acme"
	room      uint64 = 4242
	otherRoom uint64 = 777
	tick             = time.Second

	historyLostMsg = "change feed history lost; restarting from now, changes in the gap need a resync"
	dropMsg        = "dropping change that cannot become a work record"
	failedMsg      = "work record publish failed; retrying"
)

var setup = reconcile.Config{
	SubjectRoot: "work", Partitions: 4, Window: 4, Batch: 8,
	ConfirmEvery: tick, Drain: tick, Poll: tick,
}

type owner struct{ leading atomic.Bool }

func (o *owner) Owns(slot uint16) bool { return slot == reconcile.LeaderSlot && o.leading.Load() }

type busyFeed struct {
	store.ChangeFeed
	refusals atomic.Int32
}

func (b *busyFeed) Open(ctx context.Context) (store.Cursor, error) {
	if b.refusals.Add(-1) >= 0 {
		return nil, store.ErrFeedBusy
	}
	return b.ChangeFeed.Open(ctx)
}

type rig struct {
	*reconcile.Reconciler
	msgs  *memstore.Messages
	rooms *memstore.Rooms
	feed  *memstore.Feed
	base  int
	owner *owner
	js    *publishtest.JetStream
	sink  *testlog.Sink
}

func newRig(t *testing.T, wrap func(store.ChangeFeed) store.ChangeFeed) *rig {
	t.Helper()
	rg := &rig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
	rg.createRoom(t, room)
	rg.feed = memstore.NewFeed(rg.msgs, rg.rooms, nil)
	rg.base, _ = rg.feed.Confirmed()
	rg.owner.leading.Store(true)
	var feed store.ChangeFeed = rg.feed
	if wrap != nil {
		feed = wrap(feed)
	}
	rec, err := reconcile.New(reconcile.Deps{Feed: feed, Owner: rg.owner, JS: rg.js}, setup, rg.sink.Logger())
	if err != nil {
		t.Fatalf("reconcile.New: %v", err)
	}
	rg.Reconciler = rec
	return rg
}

func (rg *rig) start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rg.Run(ctx) }()
	t.Cleanup(func() {
		stop, stopped := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopped()
		if err := rg.Close(stop); err != nil {
			t.Errorf("Close: %v", err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v, want nil after Close", err)
		}
	})
	return rg
}

func (rg *rig) createRoom(t *testing.T, id uint64) {
	t.Helper()
	created := time.Now()
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rg.rooms.Create(t.Context(), r, []domain.Member{{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
}

func (rg *rig) insert(t *testing.T, r uint64, seqs ...uint64) {
	t.Helper()
	for _, s := range seqs {
		m := domain.Message{Room: r, Seq: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC()}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert %d/%d: %+v", r, s, res)
		}
	}
}

func (rg *rig) confirmed(t *testing.T) int {
	t.Helper()
	n, _ := rg.feed.Confirmed()
	return n - rg.base
}

func recordID(seq uint64) string {
	return work.Record{Kind: store.MessageInserted, Room: room, Seq: seq}.ID()
}

func roomRecordID(id uint64) string {
	return work.Record{Kind: store.RoomInserted, Room: id}.ID()
}

func recordIDs(seqs ...uint64) []string {
	out := make([]string, len(seqs))
	for i, s := range seqs {
		out[i] = recordID(s)
	}
	return out
}

func attemptIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Attempts() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

func storedIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Stored() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}
