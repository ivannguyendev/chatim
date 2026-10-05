package publish_test

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
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itNATSURLEnv = "CHATIM_IT_NATS_URL"

type itStream struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	cfg      publish.StreamConfig
	failures *testlog.Sink
}

func realStream(t *testing.T, pub publish.Config) *itStream {
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
	failures := &testlog.Sink{}
	js, err := jetstream.New(nc, pub.JetStreamOptions(failures.Logger(), nil)...)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	cfg := publish.StreamConfig{Name: "IT_EVT_" + strings.ToUpper(suffix), SubjectRoot: "itevt" + suffix, LiveRoot: "itlive" + suffix, Replicas: 1}
	if err := publish.EnsureStream(t.Context(), js, cfg); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := js.DeleteStream(ctx, cfg.Name); err != nil {
			t.Errorf("delete stream %s: %v", cfg.Name, err)
		}
	})
	if err := publish.EnsureStream(t.Context(), js, cfg); err != nil {
		t.Fatalf("EnsureStream on an existing stream: %v", err)
	}
	return &itStream{nc: nc, js: js, cfg: cfg, failures: failures}
}

func TestRealJetStreamDedupesByMsgIDAndRepublishesLive(t *testing.T) {
	cfg := fastSetup
	it := realStream(t, cfg)
	cfg.SubjectRoot = it.cfg.SubjectRoot
	live, err := it.nc.SubscribeSync(it.cfg.LiveRoot + ".acme.room.101.evt.msg_created")
	if err != nil {
		t.Fatalf("subscribe live: %v", err)
	}
	if err := it.nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	sink := &testlog.Sink{}
	p, err := publish.New(it.js, cfg, sink.Logger())
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	for _, batch := range [][]*chatimv1.Event{events(roomA, 1), events(roomA, 1), events(roomA, 2)} {
		if err := p.Enqueue(roomA, batch); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	eventually(t, "both events stored", func() bool { return streamMsgs(t, it) == 2 })
	stop, stopped := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopped()
	if err := p.Close(stop); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if n := streamMsgs(t, it); n != 2 {
		t.Fatalf("stream holds %d messages after publishing seq 1 twice and seq 2, want 2", n)
	}
	for _, seq := range []uint64{1, 2} {
		msg, err := live.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatalf("live seq %d: %v", seq, err)
		}
		got := &chatimv1.Event{}
		if err := proto.Unmarshal(msg.Data, got); err != nil {
			t.Fatalf("decode live seq %d: %v", seq, err)
		}
		if want := events(roomA, seq)[0]; !proto.Equal(got, want) {
			t.Fatalf("live event = %v, want %v", got, want)
		}
		if id := msg.Header.Get(jetstream.MsgIDHeader); id != pbconv.MessageEventID(roomA, 0, seq) {
			t.Fatalf("live msg id = %q", id)
		}
	}
	if msg, err := live.NextMsg(100 * time.Millisecond); err == nil {
		t.Fatalf("duplicate was republished live: %s", msg.Header.Get(jetstream.MsgIDHeader))
	}
}

func TestRealJetStreamReportsUnstoredPublishesThroughTheErrHandler(t *testing.T) {
	cfg := fastSetup
	it := realStream(t, cfg)
	cfg.SubjectRoot = it.cfg.SubjectRoot + "unbound"
	p, err := publish.New(it.js, cfg, nil)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- p.Run(context.Background()) }()
	if err := p.Enqueue(roomA, events(roomA, 1)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	stop, stopped := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopped()
	if err := p.Close(stop); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-it.js.PublishAsyncComplete():
	case <-stop.Done():
		t.Fatal("async publishes did not settle")
	}
	if n := it.failures.Count(failedMsg); n != 1 {
		t.Fatalf("err handler logged %d failures for a subject without a stream, want 1", n)
	}
}

func streamMsgs(t *testing.T, it *itStream) uint64 {
	t.Helper()
	s, err := it.js.Stream(t.Context(), it.cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}
