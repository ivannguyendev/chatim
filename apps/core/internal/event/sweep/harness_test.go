package sweep_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant          = "acme"
	room     uint64 = 4242
	interval        = 2 * time.Second
	minAge          = 5 * time.Second
	lookback        = 30 * time.Second
	tick            = time.Second

	startedMsg = "event sweeper started"
	staleMsg   = "pending state older than the sweep checkpoint"
)

var setup = sweep.Config{SubjectRoot: "evt", Interval: interval, MinAge: minAge, Lookback: lookback, Batch: 16, Rate: 1000, Tick: tick, AckTimeout: time.Second}

type owner struct{ leading atomic.Bool }

func (o *owner) Owns(slot uint16) bool { return slot == sweep.LeaderSlot && o.leading.Load() }

var (
	errNATS  = errors.New("nats down")
	errStore = errors.New("store down")
)

type rig struct {
	*sweep.Sweeper
	cfg        sweep.Config
	rebuilders map[store.StateCollection]sweep.Rebuilder
	msgs       *memstore.Messages
	rooms      *memstore.Rooms
	states     *memstore.EventStates
	marks      *memstore.SweepCheckpoints
	owner      *owner
	js         *publishtest.JetStream
	sink       *testlog.Sink
	done       chan error
}

func newRig(t *testing.T, cfg sweep.Config, wrap func(sweep.Rebuilder) sweep.Rebuilder) *rig {
	t.Helper()
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), marks: memstore.NewSweepCheckpoints(),
		owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}, done: make(chan error, 1),
	}
	rg.states = memstore.NewEventStates(memstore.EventStateStores{Messages: rg.msgs, Rooms: rg.rooms})
	if wrap == nil {
		wrap = func(r sweep.Rebuilder) sweep.Rebuilder { return r }
	}
	rg.cfg = cfg
	rg.rebuilders = map[store.StateCollection]sweep.Rebuilder{
		store.StateMessages: wrap(sweep.NewMessageRebuilder(rg.msgs, eventkit.NewRoomTypes(rg.rooms, 16), nil)),
		store.StateRooms:    wrap(sweep.NewRoomRebuilder(rg.rooms, nil)),
	}
	rg.replaceStates(t, rg.states)
	rg.owner.leading.Store(true)
	return rg
}

func (rg *rig) replaceStates(t *testing.T, states store.EventStates) {
	t.Helper()
	deps := sweep.Deps{States: states, Checkpoints: rg.marks, Owner: rg.owner, JS: rg.js, Rebuilders: rg.rebuilders}
	s, err := sweep.New(deps, rg.cfg, rg.sink.Logger())
	if err != nil {
		t.Fatalf("sweep.New: %v", err)
	}
	rg.Sweeper = s
}

func (rg *rig) start() {
	go func() { rg.done <- rg.Run(context.Background()) }()
	synctest.Wait()
}

func (rg *rig) nextPass() {
	time.Sleep(interval)
	synctest.Wait()
}

func (rg *rig) stop(t *testing.T) {
	t.Helper()
	if err := rg.Close(context.Background()); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := <-rg.done; err != nil {
		t.Fatalf("Run after Close = %v", err)
	}
}

func (rg *rig) createRoom(t *testing.T, id uint64, at time.Time) {
	t.Helper()
	if err := rg.rooms.InsertRoom(t.Context(), domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "ann", CreatedAt: at}); err != nil {
		t.Fatalf("InsertRoom: %v", err)
	}
}

func (rg *rig) send(t *testing.T, roomID, seq uint64, at time.Time) {
	t.Helper()
	m := domain.Message{Room: roomID, Seq: seq, Tenant: tenant, From: "ann", CID: "c" + strconv.FormatUint(seq, 10), Text: "hi", CreatedAt: at}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v", res[0])
	}
}

func (rg *rig) pending(t *testing.T, c store.StateCollection) []store.PendingDoc {
	t.Helper()
	docs, _, err := rg.states.Pending(t.Context(), c, time.Time{}, time.Now().Add(time.Hour), store.PendingCursor{}, store.MaxPendingPage)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	return docs
}

func (rg *rig) checkpoint(t *testing.T, c store.StateCollection) time.Time {
	t.Helper()
	at, err := rg.marks.Get(t.Context(), c)
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	return at
}

func (rg *rig) eventIDs(t *testing.T) []string {
	t.Helper()
	evs, err := rg.js.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	ids := make([]string, len(evs))
	for i, ev := range evs {
		ids[i] = ev.GetId()
	}
	slices.Sort(ids)
	return ids
}

func expectIDs(t *testing.T, rg *rig, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := rg.eventIDs(t); !slices.Equal(got, want) {
		t.Fatalf("published ids = %v, want %v", got, want)
	}
}

func expectPending(t *testing.T, rg *rig, c store.StateCollection, want int) {
	t.Helper()
	if got := rg.pending(t, c); len(got) != want {
		t.Fatalf("pending %s = %+v, want %d docs", c, got, want)
	}
}

func expectCheckpoint(t *testing.T, rg *rig, c store.StateCollection, want time.Time) {
	t.Helper()
	if got := rg.checkpoint(t, c); !got.Equal(want.UTC().Truncate(time.Millisecond)) {
		t.Fatalf("checkpoint %s = %v, want %v", c, got, want)
	}
}

type rebuildFunc func(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error)

func (f rebuildFunc) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	return f(ctx, doc, bit)
}
