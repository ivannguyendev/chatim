package reconcile_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	room   uint64 = 4242
	delay         = 30 * time.Second
	tick          = time.Second

	historyLostMsg = "change feed history lost; restarting from now, events in the gap are lost for good"
	dropMsg        = "dropping change that cannot become an event"
	lagMsg         = "reconciler lags behind the stream duplicate window; republished events may duplicate"
)

var setup = reconcile.Config{
	SubjectRoot: "evt", Delay: delay, DuplicateWindow: 5 * time.Minute, Window: 4, Batch: 8,
	ConfirmEvery: tick, Drain: tick, Poll: tick, RoomCache: 16,
}

type owner struct{ leading atomic.Bool }

func (o *owner) Owns(slot uint16) bool { return slot == reconcile.LeaderSlot && o.leading.Load() }

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

func (m *marks) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *marks) mark(keys ...store.MsgKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		m.acked[k] = true
	}
}

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
	feed  *memstore.Feed
	owner *owner
	marks *marks
	js    *publishtest.JetStream
	sink  *testlog.Sink
}

func newRig(t *testing.T, wrap func(store.ChangeFeed) store.ChangeFeed) *rig {
	t.Helper()
	rg := &rig{msgs: memstore.NewMessages(), owner: &owner{}, marks: &marks{acked: map[store.MsgKey]bool{}}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
	rg.feed = memstore.NewFeed(rg.msgs)
	rg.owner.leading.Store(true)
	rooms := memstore.NewRooms()
	created := time.Now()
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: room, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create room: %v", err)
	}
	var feed store.ChangeFeed = rg.feed
	if wrap != nil {
		feed = wrap(feed)
	}
	rec, err := reconcile.New(reconcile.Deps{Feed: feed, Rooms: rooms, Marks: rg.marks, Owner: rg.owner, JS: rg.js}, setup, rg.sink.Logger())
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

func (rg *rig) insert(t *testing.T, r uint64, seqs ...uint64) {
	t.Helper()
	for _, s := range seqs {
		m := domain.Message{Room: r, Seq: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC()}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert %d/%d: %+v", r, s, res)
		}
	}
}

func eventID(seq uint64) string { return pbconv.MessageEventID(room, 0, seq) }

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

func (rg *rig) confirmed(t *testing.T) int {
	t.Helper()
	n, _ := rg.feed.Confirmed()
	return n
}
