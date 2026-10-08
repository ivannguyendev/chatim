package actor_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func startPublisher(t *testing.T, js publish.JetStream) *publish.Publisher {
	t.Helper()
	p, err := publish.New(js, publish.Config{SubjectRoot: "evt"}, quiet)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		stop, stopped := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopped()
		if err := p.Close(stop); err != nil {
			t.Errorf("publisher Close: %v", err)
		}
		cancel()
		<-done
	})
	return p
}

func drain(t *testing.T, closers ...func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range closers {
		if err := c(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
}

func TestSentMessagesArePublishedInSeqOrder(t *testing.T) {
	w := newWorld(t)
	createRoom(t, w.rooms, roomB, "alice", "bob")
	js := &publishtest.JetStream{}
	pub := startPublisher(t, js)
	core := startCoreWith(t, w.msgs, w.rooms, &fakeRegistry{}, pub)
	const perRoom = 25
	rooms := []uint64{roomA, roomB}
	var wg sync.WaitGroup
	for _, room := range rooms {
		for i := range perRoom {
			wg.Go(func() {
				c := cmd(room, []string{"alice", "bob"}[i%2], fmt.Sprintf("m%d", i))
				if _, attempts, err := sendRetrying(core, c); err != nil {
					t.Errorf("Send %d/%s: %v after %d attempts", room, c.CID, err, attempts)
				}
			})
		}
	}
	wg.Wait()
	drain(t, core.Close, pub.Close)

	for _, room := range rooms {
		docs := timeline(t, w.msgs, room)
		var got []proto.Message
		evs, err := js.Events()
		if err != nil {
			t.Fatalf("decode published events: %v", err)
		}
		for _, ev := range evs {
			if ev.GetRoomId() == pbconv.RoomID(room) {
				got = append(got, ev)
			}
		}
		if len(docs) != perRoom || len(got) != perRoom {
			t.Fatalf("room %d: stored %d, published %d, want %d each", room, len(docs), len(got), perRoom)
		}
		for i, doc := range docs {
			if want := pbconv.MessageCreated(domain.RoomGroup, doc); !proto.Equal(got[i], want) {
				t.Fatalf("room %d event %d = %v, want %v", room, i, got[i], want)
			}
		}
	}
}
