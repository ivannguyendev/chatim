package projection_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itNATSURLEnv = "CHATIM_IT_NATS_URL"

type signallingRooms struct {
	*memstore.Rooms
	touched chan []store.Activity
}

func (s signallingRooms) TouchActivity(ctx context.Context, acts []store.Activity) error {
	err := s.Rooms.TouchActivity(ctx, acts)
	select {
	case s.touched <- acts:
	case <-ctx.Done():
	}
	return err
}

func realEventStream(t *testing.T) (jetstream.JetStream, projection.Config) {
	t.Helper()
	url := os.Getenv(itNATSURLEnv)
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	sc := publish.StreamConfig{Name: "IT_EVT_" + strings.ToUpper(suffix), SubjectRoot: "itevt" + suffix, LiveRoot: "itlive" + suffix, Replicas: 1}
	if err := publish.EnsureStream(t.Context(), js, sc); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := js.DeleteStream(ctx, sc.Name); err != nil {
			t.Errorf("delete stream %s: %v", sc.Name, err)
		}
	})
	return js, projection.Config{Stream: sc.Name, SubjectRoot: sc.SubjectRoot}
}

func TestRoomActivityProjectionTouchesRoomsFromRealEvents(t *testing.T) {
	js, cfg := realEventStream(t)
	rooms := signallingRooms{Rooms: memstore.NewRooms(), touched: make(chan []store.Activity, 8)}
	for _, id := range []uint64{roomA, roomB, roomC} {
		if err := rooms.InsertRoom(t.Context(), domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, CreatedAt: baseTime}); err != nil {
			t.Fatalf("InsertRoom: %v", err)
		}
	}
	p := projection.NewRoomActivity(rooms)
	for range 2 {
		if err := projection.EnsureConsumer(t.Context(), js, cfg, p); err != nil {
			t.Fatalf("EnsureConsumer: %v", err)
		}
	}
	for _, ev := range []*chatimv1.Event{readUpdated(roomA, at(9)), msgCreated(roomA, 0, 1, at(1)), reactionChanged(roomB, 1, at(2)), msgEdited(roomC, 0, 1, at(3))} {
		m, err := publish.Message(cfg.SubjectRoot, mustRoom(t, ev), ev)
		if err != nil {
			t.Fatalf("publish.Message: %v", err)
		}
		if _, err := js.PublishMsg(t.Context(), m); err != nil {
			t.Fatalf("PublishMsg %s: %v", m.Subject, err)
		}
	}
	c := built(projection.NewConsumer(p, projection.NewSource(js, cfg, p), cfg, (&testlog.Sink{}).Logger()))(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.Run(ctx) }()
	awaitTouched(t, rooms.touched, 3)
	if err := c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Run = %v", err)
	}
	assertRoomActivity(t, rooms.Rooms)
	assertSettled(t, js, cfg, p)
}

func mustRoom(t *testing.T, ev *chatimv1.Event) uint64 {
	t.Helper()
	d, err := projection.Decode(encoded(t, ev))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return d.Room
}

func awaitTouched(t *testing.T, touched <-chan []store.Activity, want int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	seen := 0
	for seen < want {
		select {
		case acts := <-touched:
			seen += len(acts)
		case <-deadline:
			t.Fatalf("touched %d rooms within 10s, want %d", seen, want)
		}
	}
}

func assertRoomActivity(t *testing.T, rooms *memstore.Rooms) {
	t.Helper()
	for id, want := range map[uint64]roomTrace{
		roomA: {seq: 1, msgAt: at(1), changed: true},
		roomB: {changed: true},
		roomC: {changed: true, edited: true},
	} {
		r, err := rooms.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get %d: %v", id, err)
		}
		if got := traceOf(r); got != want {
			t.Errorf("room %d activity %+v, want %+v", id, got, want)
		}
	}
}

func assertSettled(t *testing.T, js jetstream.JetStream, cfg projection.Config, p projection.Projection) {
	t.Helper()
	cons, err := js.Consumer(t.Context(), cfg.Stream, projection.DurableName(p.Name()))
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := cons.Info(t.Context())
		if err != nil {
			t.Fatalf("consumer info: %v", err)
		}
		settled := info.NumAckPending == 0 && info.NumPending == 0 && info.Delivered.Consumer == 3
		if settled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("consumer pending %d ack pending %d delivered %d; want the 3 kept events delivered and acked, read_updated filtered out", info.NumPending, info.NumAckPending, info.Delivered.Consumer)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
