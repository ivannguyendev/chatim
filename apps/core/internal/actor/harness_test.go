package actor_test

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	roomA  uint64 = 101
	roomB  uint64 = 202
)

var (
	quiet      = slog.New(slog.DiscardHandler)
	baseConfig = actor.Config{Mailbox: 16, Idle: time.Minute, MaxGroup: 8, MaxActors: 8, GroupDeadline: time.Second, ReservationTTL: 10 * time.Second}
)

type rig struct {
	*actor.Router
	msgs   *spyMessages
	rooms  *spyRooms
	sub    *fakeSubmitter
	cids   *fakeRegistry
	events *publishSpy
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	runErr error
}

func newRig(t *testing.T, cfg actor.Config, opts ...actor.Option) *rig {
	t.Helper()
	base := memstore.NewMessages()
	rg := &rig{
		msgs:   &spyMessages{Messages: base},
		rooms:  &spyRooms{Rooms: memstore.NewRooms()},
		sub:    &fakeSubmitter{store: base},
		cids:   &fakeRegistry{},
		events: &publishSpy{},
		done:   make(chan error, 1),
	}
	createRoom(t, rg.rooms, roomA, "alice", "bob")
	createRoom(t, rg.rooms, roomB, "alice", "bob")
	r, err := actor.NewRouter(rg.msgs, rg.rooms, rg.sub, rg.cids, rg.events, cfg, quiet, opts...)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	rg.Router = r
	return rg
}

func (rg *rig) start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rg.cancel = cancel
	go func() { rg.done <- rg.Run(ctx) }()
	for !rg.Started() {
		runtime.Gosched()
	}
	t.Cleanup(func() {
		cancel()
		_ = rg.wait()
		rg.sub.open()
	})
	return rg
}

func (rg *rig) wait() error {
	rg.once.Do(func() { rg.runErr = <-rg.done })
	return rg.runErr
}

func started(t *testing.T, cfg actor.Config) *rig {
	t.Helper()
	return newRig(t, cfg).start(t)
}

func createRoom(t *testing.T, rooms store.Rooms, id uint64, users ...string) {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	room, members, err := domain.NewRoom(tenant, users[0], domain.RoomGroup, "Team", users, now, id)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rooms.Create(context.Background(), room, members); err != nil {
		t.Fatalf("Create room %d: %v", id, err)
	}
}

func cmd(room uint64, user, cid string) actor.SendCmd {
	return actor.SendCmd{Tenant: tenant, User: user, Room: room, CID: cid, Text: "hello " + cid}
}

func mustSend(t *testing.T, r *actor.Router, c actor.SendCmd) actor.Ack {
	t.Helper()
	ack, err := r.Send(context.Background(), c)
	if err != nil {
		t.Fatalf("Send(%s/%s): %v", c.User, c.CID, err)
	}
	return ack
}

type sendResult struct {
	ack actor.Ack
	err error
}

func sendAsync(ctx context.Context, r *actor.Router, c actor.SendCmd) <-chan sendResult {
	out := make(chan sendResult, 1)
	go func() {
		ack, err := r.Send(ctx, c)
		out <- sendResult{ack: ack, err: err}
	}()
	return out
}

func expectErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func timeline(t *testing.T, m store.Messages, room uint64) []domain.Message {
	t.Helper()
	var out []domain.Message
	var after uint64
	for {
		page, err := m.Page(context.Background(), store.PageQuery{Room: room, Anchor: store.After, Seq: after, Limit: store.MaxPageLimit})
		if err != nil {
			t.Fatalf("Page(room %d after %d): %v", room, after, err)
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, page...)
		after = page[len(page)-1].Seq
	}
}

func storedCIDs(t *testing.T, m store.Messages, room uint64) map[string][]domain.Message {
	t.Helper()
	out := map[string][]domain.Message{}
	for _, msg := range timeline(t, m, room) {
		out[msg.CID] = append(out[msg.CID], msg)
	}
	return out
}

func assertAckMatches(t *testing.T, ack actor.Ack, doc domain.Message) {
	t.Helper()
	if ack.Seq != doc.Seq || !ack.CreatedAt.Equal(doc.CreatedAt) {
		t.Fatalf("ack %+v does not match stored seq=%d at=%v", ack, doc.Seq, doc.CreatedAt)
	}
}

func sameAck(a, b actor.Ack) bool {
	return a.Seq == b.Seq && a.CreatedAt.Equal(b.CreatedAt)
}
