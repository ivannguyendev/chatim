package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itTenant      = "acme"
	itUser        = "alice"
	itSendLimit   = 5 * time.Second
	itRetryPause  = 5 * time.Millisecond
	itAckWaitTime = 30 * time.Second
)

type sent struct {
	room uint64
	cid  string
	seq  uint64
}

type load struct {
	client   chatimv1.CoreServiceClient
	rooms    []string
	halt     chan struct{}
	halted   sync.Once
	stopped  atomic.Bool
	late     atomic.Int64
	senders  sync.WaitGroup
	mu       sync.Mutex
	acks     []sent
	failures atomic.Int64
}

func caller(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, grpcsrv.TenantHeader, itTenant, grpcsrv.UserHeader, itUser)
}

func startLoad(client chatimv1.CoreServiceClient, rooms []string, senders int) *load {
	l := &load{client: client, rooms: rooms, halt: make(chan struct{})}
	for w := range senders {
		l.senders.Go(func() { l.send(w) })
	}
	return l
}

func (l *load) send(w int) {
	room := l.rooms[w%len(l.rooms)]
	id, _ := ids.ParseRoomID(room)
	ctx := caller(context.Background())
	for i := 0; ; i++ {
		select {
		case <-l.halt:
			return
		default:
		}
		cid := fmt.Sprintf("w%d-m%d", w, i)
		late := l.stopped.Load()
		cctx, cancel := context.WithTimeout(ctx, itSendLimit)
		resp, err := l.client.SendMessage(cctx, &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: "load " + cid})
		cancel()
		if late {
			l.late.Add(1)
		}
		if err != nil {
			l.failures.Add(1)
			time.Sleep(itRetryPause)
			continue
		}
		l.mu.Lock()
		l.acks = append(l.acks, sent{room: id, cid: cid, seq: resp.GetSeq()})
		l.mu.Unlock()
	}
}

func (l *load) awaitAcks(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(itAckWaitTime)
	for len(l.acked()) < n {
		if time.Now().After(deadline) {
			l.stop()
			t.Fatalf("only %d of %d sends acked within %v (%d failed)", len(l.acked()), n, itAckWaitTime, l.failures.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.stopped.Store(true)
}

func (l *load) acked() []sent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]sent(nil), l.acks...)
}

func (l *load) afterStop() int64 { return l.late.Load() }

func (l *load) stop() {
	l.halted.Do(func() { close(l.halt) })
	l.senders.Wait()
}
