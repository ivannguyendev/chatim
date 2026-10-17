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
	index := newIndexRig(t)
	mentioning := func(m *domain.Message) { m.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}} }
	events := []*chatimv1.Event{
		index.send(t, 1, mentioning).Proto, index.send(t, 2, replyTo(1)).Proto, reactionChanged(roomB, 1, at(2)),
		msgEdited(roomC, 0, 1, at(3)), roomCreated(roomB),
	}
	subjects := publishAll(t, js, cfg, events)
	for _, data := range []string{".mention.", ".message."} {
		if !slices.ContainsFunc(subjects, func(s string) bool { return strings.Contains(s, data) }) {
			t.Fatalf("subjects %v carry no %s data", subjects, data)
		}
	}
	hs := []projection.Projection{projection.NewRoomActivity(rooms), index.index}
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
	for id, want := range map[uint64]roomTrace{roomA: {seq: 2, msgAt: indexedAt, changed: true}, roomB: {changed: true}, roomC: {changed: true, edited: true}} {
		r, err := rooms.Get(t.Context(), id)
		if err != nil || traceOf(r) != want {
			t.Errorf("room %d activity %+v (%v), want %+v", id, traceOf(r), err, want)
		}
	}
	if got := index.liveReplies(t, 1); got != 1 {
		t.Errorf("live replies of seq 1 = %d, want the reply indexed", got)
	}
}

func roomCreated(room uint64) *chatimv1.Event {
	ev := envelope(room, 0, 0, at(1))
	ev.Payload = &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{}}
	return ev
}

func publishAll(t *testing.T, js jetstream.JetStream, cfg projection.Config, events []*chatimv1.Event) []string {
	t.Helper()
	subjects := make([]string, 0, len(events))
	for _, ev := range events {
		m, err := publish.Message(cfg.SubjectRoot, mustRoom(t, ev), ev)
		if err != nil {
			t.Fatalf("publish.Message: %v", err)
		}
		if _, err := js.PublishMsg(t.Context(), m); err != nil {
			t.Fatalf("PublishMsg %s: %v", m.Subject, err)
		}
		subjects = append(subjects, m.Subject)
	}
	return subjects
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
		if info.Config.AckPolicy != jetstream.AckExplicitPolicy || info.Config.FilterSubject != cfg.SubjectRoot+".>" {
			t.Fatalf("durable config %+v, want explicit ack on %s.>", info.Config, cfg.SubjectRoot)
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
