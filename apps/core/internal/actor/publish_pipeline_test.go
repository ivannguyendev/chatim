package actor_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func startPublisher(t *testing.T, js publish.JetStream, rdb *redis.Client) *publish.Publisher {
	t.Helper()
	p, err := publish.New(js, rdb, publish.Config{SubjectRoot: "evt", FlushEvery: time.Millisecond, RedisTimeout: time.Second}, quiet)
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

func marksFor(t *testing.T, rdb *redis.Client) *publish.ActivityMarks {
	t.Helper()
	m, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second, Cooldown: 20 * time.Millisecond}, quiet)
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	return m
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

func TestSentMessagesArePublishedInPtsOrderUpToTheWatermark(t *testing.T) {
	w := newWorld(t)
	createRoom(t, w.rooms, roomB, "alice", "bob")
	js := &publishtest.JetStream{}
	pub := startPublisher(t, js, w.rdb)
	core := startCoreWith(t, w.msgs, w.rooms, &fakeRegistry{}, pub, marksFor(t, w.rdb))
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
		if v, _ := w.mr.Get(publish.WatermarkKey(room)); v != strconv.Itoa(perRoom) {
			t.Fatalf("room %d watermark after drain = %q, want %d", room, v, perRoom)
		}
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
		if _, err := w.mr.ZScore(publish.ActiveKey(slotmap.Of(room)), pbconv.RoomID(room)); err != nil {
			t.Fatalf("room %d not marked active: %v", room, err)
		}
	}
}

func TestSendSucceedsWhileRedisIsDownForActiveMarks(t *testing.T) {
	w := newWorld(t)
	core := startCoreWith(t, w.msgs, w.rooms, &fakeRegistry{}, nopPublisher{}, marksFor(t, w.rdb))
	w.mr.Close()
	for i := range 3 {
		mustSend(t, core, cmd(roomA, "alice", fmt.Sprintf("x%d", i)))
	}
	if n := len(timeline(t, w.msgs, roomA)); n != 3 {
		t.Fatalf("stored %d messages with redis down, want 3", n)
	}
}
