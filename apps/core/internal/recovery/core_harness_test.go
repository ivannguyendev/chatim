package recovery_test

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	routerSetup  = actor.Config{Mailbox: 64, Idle: time.Minute, MaxGroup: 16, MaxActors: 64, GroupDeadline: time.Second, ReservationTTL: 10 * time.Second}
	flushSetup   = flush.Config{Shards: 2, Window: time.Millisecond, MaxBatch: 64, QueueSize: 64, InsertTimeout: 500 * time.Millisecond}
	publishSetup = publish.Config{SubjectRoot: "evt", Attempts: 1, RetryBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond, FlushEvery: time.Millisecond, RedisTimeout: time.Second}
)

type world struct {
	msgs  store.Messages
	rooms store.Rooms
	mr    *miniredis.Miniredis
	rdb   *redis.Client
	pub   publish.Config
}

func newWorld(t *testing.T) *world {
	t.Helper()
	mr, rdb := newRedis(t)
	w := &world{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), mr: mr, rdb: rdb, pub: publishSetup}
	createRoom(t, w.rooms, roomA)
	return w
}

func createRoom(t *testing.T, rooms store.Rooms, id uint64) {
	t.Helper()
	room, members, err := domain.NewRoom(tenant, "alice", domain.RoomGroup, "Team", []string{"alice", "bob"}, time.Now().UTC(), id)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rooms.Create(context.Background(), room, members); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
}

func (w *world) watermark(room uint64) (uint64, bool) {
	v, err := w.rdb.Get(context.Background(), publish.WatermarkKey(room)).Result()
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	return n, err == nil
}

type core struct {
	router *actor.Router
	pub    *publish.Publisher
	cancel context.CancelFunc
	wg     sync.WaitGroup
	stop   func()
}

type lostEvents struct{}

func (lostEvents) Enqueue(uint64, []*chatimv1.Event) error { return nil }

func (lostEvents) Skip(uint64, []uint64) error { return nil }

func (w *world) startCore(t *testing.T, id string, js publish.JetStream) *core {
	t.Helper()
	flusher, err := flush.New(w.msgs, flushSetup)
	if err != nil {
		t.Fatalf("flush.New: %v", err)
	}
	var pub *publish.Publisher
	var events actor.EventPublisher = lostEvents{}
	if js != nil {
		if pub, err = publish.New(js, w.rdb, w.pub, quiet); err != nil {
			t.Fatalf("publish.New: %v", err)
		}
		events = pub
	}
	marks, err := publish.NewActivityMarks(w.rdb, publish.MarkConfig{Timeout: time.Second, Cooldown: 20 * time.Millisecond}, quiet)
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	cids, err := dedupe.New(w.rdb, dedupe.Config{CoreID: id, Timeout: time.Second}, quiet)
	if err != nil {
		t.Fatalf("dedupe.New: %v", err)
	}
	router, err := actor.NewRouter(w.msgs, w.rooms, flusher, cids, events, marks, routerSetup, quiet)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &core{router: router, pub: pub, cancel: cancel}
	c.wg.Go(func() { _ = flusher.Run(ctx) })
	if pub != nil {
		c.wg.Go(func() { _ = pub.Run(ctx) })
	}
	c.wg.Go(func() { _ = router.Run(ctx) })
	for !router.Started() {
		runtime.Gosched()
	}
	c.stop = sync.OnceFunc(func() {
		cancel()
		c.wg.Wait()
	})
	t.Cleanup(c.stop)
	return c
}

func (c *core) drain(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.router.Close(ctx); err != nil {
		t.Fatalf("router Close: %v", err)
	}
	if c.pub != nil {
		if err := c.pub.Close(ctx); err != nil {
			t.Fatalf("publisher Close: %v", err)
		}
	}
	c.stop()
}

func sendMany(t *testing.T, r *actor.Router, room uint64, n int) {
	t.Helper()
	const senders = 5
	var wg sync.WaitGroup
	for s := range senders {
		wg.Go(func() {
			for i := s; i < n; i += senders {
				c := actor.SendCmd{Tenant: tenant, User: "alice", Room: room, CID: fmt.Sprintf("m%d", i), Text: "hello"}
				if _, err := r.Send(context.Background(), c); err != nil {
					t.Errorf("Send %s: %v", c.CID, err)
				}
			}
		})
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
}

func eventIDs(room uint64, from, to uint64) []string {
	var out []string
	for p := from; p <= to; p++ {
		out = append(out, pbconv.EventID(room, p))
	}
	return out
}

func idsOf(msgs []*nats.Msg) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = publishtest.MsgID(m)
	}
	return out
}
