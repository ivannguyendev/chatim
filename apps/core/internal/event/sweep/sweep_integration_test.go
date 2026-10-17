package sweep_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itRoom uint64 = 7_350_000_001

type alwaysLeader struct{}

func (alwaysLeader) Owns(uint16) bool { return true }

func itSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func itMongoStore(t *testing.T) *mongostore.Store {
	t.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" || os.Getenv("CHATIM_IT_NATS_URL") == "" {
		t.Skip("set CHATIM_IT_MONGO_URI and CHATIM_IT_NATS_URL to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	db := client.Database("chatim_it_" + itSuffix())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", db.Name(), err)
		}
	})
	if err := mongostore.Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return mongostore.New(db, mongostore.Options{})
}

func itEventStream(t *testing.T) (jetstream.JetStream, publish.StreamConfig) {
	t.Helper()
	nc, err := nats.Connect(os.Getenv("CHATIM_IT_NATS_URL"))
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	suffix := itSuffix()
	sc := publish.StreamConfig{Name: "IT_SWEEP_" + strings.ToUpper(suffix), SubjectRoot: "itsweep" + suffix, LiveRoot: "itsweeplive" + suffix, Replicas: 1}
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
	return js, sc
}

func streamIDs(t *testing.T, js jetstream.JetStream, name string) []string {
	t.Helper()
	stream, err := js.Stream(t.Context(), name)
	if err != nil {
		t.Fatalf("stream %s: %v", name, err)
	}
	var ids []string
	for seq := uint64(1); seq <= stream.CachedInfo().State.LastSeq; seq++ {
		raw, err := stream.GetMsg(t.Context(), seq)
		if err != nil {
			t.Fatalf("GetMsg %d: %v", seq, err)
		}
		ev := &chatimv1.Event{}
		if err := proto.Unmarshal(raw.Data, ev); err != nil {
			t.Fatalf("decode %d: %v", seq, err)
		}
		ids = append(ids, ev.GetId())
	}
	slices.Sort(ids)
	return ids
}

func TestSweepRepublishesMessagesFlaggedByTheRealStore(t *testing.T) {
	st := itMongoStore(t)
	js, sc := itEventStream(t)
	old := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	if err := st.InsertRoom(t.Context(), domain.Room{ID: itRoom, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "ann", CreatedAt: old}); err != nil {
		t.Fatalf("InsertRoom: %v", err)
	}
	var msgs []domain.Message
	for seq := uint64(1); seq <= 3; seq++ {
		msgs = append(msgs, domain.Message{Room: itRoom, Seq: seq, Tenant: tenant, From: "ann", CID: "c" + pbconv.RoomID(seq), Text: "hi", CreatedAt: old})
	}
	for _, res := range st.Insert(t.Context(), msgs) {
		if res.Outcome != store.Inserted {
			t.Fatalf("Insert = %+v", res)
		}
	}
	rebuilders := map[store.StateCollection]sweep.Rebuilder{
		store.StateMessages: sweep.NewMessageRebuilder(st, eventkit.NewRoomTypes(st, 16), nil),
		store.StateRooms:    sweep.NewRoomRebuilder(st, nil),
	}
	deps := sweep.Deps{States: st.EventStates(), Checkpoints: st.SweepCheckpoints(), Owner: alwaysLeader{}, JS: js, Rebuilders: rebuilders}
	cfg := sweep.Config{SubjectRoot: sc.SubjectRoot, MinAge: time.Millisecond, Interval: 50 * time.Millisecond, Tick: 50 * time.Millisecond, AckTimeout: 5 * time.Second}
	sink := &testlog.Sink{}
	s, err := sweep.New(deps, cfg, sink.Logger())
	if err != nil {
		t.Fatalf("sweep.New: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	deadline := time.Now().Add(10 * time.Second)
	for s.Passes() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Passes() == 0 || sink.Count(startedMsg) != 1 {
		t.Fatalf("passes %d, %q logged %d times; want a finished pass", s.Passes(), startedMsg, sink.Count(startedMsg))
	}
	want := []string{pbconv.RoomCreatedEventID(itRoom), pbconv.MessageEventID(itRoom, 0, 1), pbconv.MessageEventID(itRoom, 0, 2), pbconv.MessageEventID(itRoom, 0, 3)}
	slices.Sort(want)
	if got := streamIDs(t, js, sc.Name); !slices.Equal(got, want) {
		t.Fatalf("stream ids = %v, want %v", got, want)
	}
	for _, c := range []store.StateCollection{store.StateMessages, store.StateRooms} {
		docs, _, err := st.EventStates().Pending(t.Context(), c, time.Time{}, time.Now(), store.PendingCursor{}, 10)
		if err != nil || len(docs) != 0 {
			t.Fatalf("pending %s after the sweep = %+v, %v; want none", c, docs, err)
		}
	}
}
