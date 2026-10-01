package actor_test

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const (
	degradedMsg  = "cid dedupe degraded to the local cache"
	recoveredMsg = "cid dedupe recovered"
)

type world struct {
	msgs  *memstore.Messages
	rooms *memstore.Rooms
	mr    *miniredis.Miniredis
	rdb   *redis.Client
}

func newWorld(t *testing.T) *world {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })
	w := &world{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), mr: mr, rdb: rdb}
	createRoom(t, w.rooms, roomA, "alice", "bob")
	return w
}

func (w *world) registry(t *testing.T, core string, log *slog.Logger, cooldown time.Duration) *dedupe.Store {
	t.Helper()
	s, err := dedupe.New(w.rdb, dedupe.Config{CoreID: core, Timeout: time.Second, Cooldown: cooldown}, log)
	if err != nil {
		t.Fatalf("dedupe.New(%s): %v", core, err)
	}
	return s
}

func (w *world) cidValue(cid string) string {
	v, _ := w.mr.Get(remoteKey(roomA, "alice", cid).String())
	return v
}

func runRouter(t *testing.T, msgs store.Messages, rooms store.Rooms, sub actor.Submitter, cids actor.CIDRegistry, cfg actor.Config) (*actor.Router, func()) {
	t.Helper()
	r, err := actor.NewRouter(msgs, rooms, sub, cids, cfg, quiet)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Run(ctx)
	}()
	for !r.Started() {
		runtime.Gosched()
	}
	stop := sync.OnceFunc(func() {
		cancel()
		<-done
	})
	t.Cleanup(stop)
	return r, stop
}

type blackhole struct{}

func (blackhole) Submit(context.Context, flush.Group) error { return nil }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within 5s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
