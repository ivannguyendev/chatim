package actor_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var (
	clusterRooms  = []uint64{roomA, roomB, 303}
	clusterUsers  = []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6", "u7"}
	clusterConfig = actor.Config{Mailbox: 64, Idle: time.Minute, MaxGroup: 8, MaxActors: 16, GroupDeadline: 2 * time.Second, ReservationTTL: 10 * time.Second}
	flushConfig   = flush.Config{Shards: 4, Window: 200 * time.Microsecond, MaxBatch: 64, QueueSize: 1024, InsertTimeout: time.Second}
)

type cluster struct {
	msgs  *memstore.Messages
	cores []*actor.Router
}

func newCluster(t *testing.T, n int) *cluster {
	t.Helper()
	cl := &cluster{msgs: memstore.NewMessages()}
	rooms := memstore.NewRooms()
	for _, id := range clusterRooms {
		createRoom(t, rooms, id, clusterUsers...)
	}
	for range n {
		cl.cores = append(cl.cores, startCore(t, cl.msgs, rooms, &fakeRegistry{}))
	}
	return cl
}

func startCore(t *testing.T, msgs store.Messages, rooms store.Rooms, cids actor.CIDRegistry) *actor.Router {
	t.Helper()
	fl, err := flush.New(msgs, flushConfig)
	if err != nil {
		t.Fatalf("flush.New: %v", err)
	}
	r, err := actor.NewRouter(msgs, rooms, fl, cids, clusterConfig, quiet)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var flushErr, routerErr error
	wg.Go(func() { flushErr = fl.Run(ctx) })
	wg.Go(func() { routerErr = r.Run(ctx) })
	for !r.Started() {
		runtime.Gosched()
	}
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := r.Close(stop); err != nil {
			t.Errorf("router Close: %v", err)
		}
		if err := fl.Close(stop); err != nil {
			t.Errorf("flusher Close: %v", err)
		}
		cancel()
		wg.Wait()
		if flushErr != nil || routerErr != nil {
			t.Errorf("Run after Close: flusher %v, router %v", flushErr, routerErr)
		}
	})
	return r
}

func sendRetrying(r *actor.Router, c actor.SendCmd) (actor.Ack, int, error) {
	for attempt := 1; ; attempt++ {
		ack, err := r.Send(context.Background(), c)
		if err == nil || attempt == 50 || (!errors.Is(err, domain.ErrRetryLater) && !errors.Is(err, domain.ErrBusy)) {
			return ack, attempt, err
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCompetingCoresKeepEveryTimelineGapless(t *testing.T) {
	cl := newCluster(t, 2)
	const workers, perWorker = 16, 30
	var mu sync.Mutex
	acks := map[string]actor.Ack{}
	roomOf := map[string]uint64{}
	retries := 0
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := range perWorker {
				room := clusterRooms[(w+i)%len(clusterRooms)]
				c := actor.SendCmd{Tenant: tenant, User: clusterUsers[w%len(clusterUsers)], Room: room, CID: fmt.Sprintf("w%d-%d", w, i), Text: "hi"}
				ack, attempts, err := sendRetrying(cl.cores[(w+i/3)%2], c)
				if err != nil {
					t.Errorf("Send %s: %v after %d attempts", c.CID, err, attempts)
					return
				}
				mu.Lock()
				acks[c.CID], roomOf[c.CID] = ack, room
				retries += attempts - 1
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	t.Logf("%d messages acked, %d client retries", len(acks), retries)

	stored := 0
	for _, room := range clusterRooms {
		seen := map[string]bool{}
		for i, doc := range timeline(t, cl.msgs, room) {
			if want := uint64(i + 1); doc.Seq != want || doc.Pts != doc.Seq {
				t.Fatalf("room %d position %d: seq %d pts %d, want seq=pts=%d", room, i, doc.Seq, doc.Pts, want)
			}
			if seen[doc.CID] {
				t.Fatalf("room %d: cid %s stored twice", room, doc.CID)
			}
			seen[doc.CID] = true
			ack, ok := acks[doc.CID]
			if !ok || roomOf[doc.CID] != room {
				t.Fatalf("room %d seq %d: cid %s stored but never acked for this room", room, doc.Seq, doc.CID)
			}
			assertAckMatches(t, ack, doc)
			stored++
		}
	}
	if stored != len(acks) || stored != workers*perWorker {
		t.Fatalf("stored %d messages, acked %d, sent %d", stored, len(acks), workers*perWorker)
	}
}
