package projection_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itNATSURLEnv = "CHATIM_IT_NATS_URL"

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
	return js, projection.Config{Stream: sc.Name, SubjectRoot: sc.SubjectRoot, FetchWait: 200 * time.Millisecond}
}

func TestProjectionsConsumerRunsEveryHandlerFromRealEventsAndSettlesTheDurable(t *testing.T) {
	js, cfg := realEventStream(t)
	legacy := jetstream.ConsumerConfig{Durable: projection.LegacyDurables[0], FilterSubject: cfg.SubjectRoot + ".>", AckPolicy: jetstream.AckExplicitPolicy}
	if _, err := js.CreateOrUpdateConsumer(t.Context(), cfg.Stream, legacy); err != nil {
		t.Fatalf("create legacy durable: %v", err)
	}
	for range 2 {
		if err := projection.EnsureConsumer(t.Context(), js, cfg); err != nil {
			t.Fatalf("EnsureConsumer: %v", err)
		}
	}
	if _, err := js.Consumer(t.Context(), cfg.Stream, projection.LegacyDurables[0]); !errors.Is(err, jetstream.ErrConsumerNotFound) {
		t.Fatalf("legacy durable lookup = %v, want it deleted", err)
	}
	rooms := memstore.NewRooms()
	for _, id := range []uint64{roomA, roomB, roomC} {
		if err := rooms.InsertRoom(t.Context(), domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, CreatedAt: baseTime}); err != nil {
			t.Fatalf("InsertRoom: %v", err)
		}
	}
	index, reads := newIndexRig(t), newReadRig(t)
	events := []*chatimv1.Event{
		reads.move(t, roomA, true, 95).Proto, index.send(t, 1, mentioning).Proto, reactionChanged(roomB, 1, at(2)),
		msgEdited(roomC, 0, 1, at(3)), roomCreated(roomB),
	}
	publishAll(t, js, cfg, events)
	hs := []projection.Projection{projection.NewRoomActivity(rooms), index.index, reads.read}
	c := built(projection.NewConsumer(hs, projection.NewSource(js, cfg), cfg, (&testlog.Sink{}).Logger()))(t)
	result := make(chan error, 1)
	go func() { result <- c.Run(t.Context()) }()
	awaitSettled(t, js, cfg, uint64(len(events)))
	if err := c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Run = %v", err)
	}
	for id, want := range map[uint64]roomTrace{roomA: {seq: 1, msgAt: indexedAt, changed: true}, roomB: {changed: true}, roomC: {changed: true, edited: true}} {
		r, err := rooms.Get(t.Context(), id)
		if err != nil || traceOf(r) != want {
			t.Errorf("room %d activity %+v (%v), want %+v", id, traceOf(r), err, want)
		}
	}
	if m := index.mentionsOf(t, 1); !m[mentionMinh].Live {
		t.Errorf("mentions of seq 1 = %+v, want minh indexed", m)
	}
	if got := reads.readSeqs(t); !slices.Equal(got, []uint64{80, 90}) {
		t.Errorf("read mentions = %v, want [80 90]", got)
	}
}

func roomCreated(room uint64) *chatimv1.Event {
	ev := envelope(room, 0, 0, at(1))
	ev.Payload = &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{}}
	return ev
}

func publishAll(t *testing.T, js jetstream.JetStream, cfg projection.Config, events []*chatimv1.Event) {
	t.Helper()
	for _, ev := range events {
		m, err := publish.Message(cfg.SubjectRoot, mustRoom(t, ev), ev)
		if err != nil {
			t.Fatalf("publish.Message: %v", err)
		}
		if _, err := js.PublishMsg(t.Context(), m); err != nil {
			t.Fatalf("PublishMsg %s: %v", m.Subject, err)
		}
	}
}

func mustRoom(t *testing.T, ev *chatimv1.Event) uint64 {
	t.Helper()
	d, err := projection.Decode(encoded(t, ev))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return d.Room
}

func awaitSettled(t *testing.T, js jetstream.JetStream, cfg projection.Config, last uint64) {
	t.Helper()
	cons, err := js.Consumer(t.Context(), cfg.Stream, projection.DurableName)
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		info, err := cons.Info(t.Context())
		if err != nil {
			t.Fatalf("consumer info: %v", err)
		}
		if info.Config.AckPolicy != jetstream.AckAllPolicy || info.Config.FilterSubject != cfg.SubjectRoot+".>" {
			t.Fatalf("durable config %+v, want ack all on %s.>", info.Config, cfg.SubjectRoot)
		}
		if info.NumAckPending == 0 && info.NumPending == 0 && info.AckFloor.Stream == last {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %d ack pending %d ack floor %d; want all %d events acked", info.NumPending, info.NumAckPending, info.AckFloor.Stream, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
