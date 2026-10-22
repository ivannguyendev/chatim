package feed

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	itRooms   = 5
	itEvents  = 500
	itTimeout = 30 * time.Second
)

type itHandler struct {
	gate    chan struct{}
	first   chan struct{}
	all     chan struct{}
	publish chan Envelope
	once    sync.Once
	mu      sync.Mutex
	rooms   map[string][]uint64
	count   int
}

func (h *itHandler) Event(e Envelope) {
	h.once.Do(func() { close(h.first) })
	<-h.gate
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rooms[e.Room] = append(h.rooms[e.Room], e.Seq)
	if h.count++; h.count == itEvents {
		close(h.all)
	}
}

func (h *itHandler) Publish(e Envelope) { h.publish <- e }

func itStreams(t *testing.T, url string) (jetstream.JetStream, Config) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	cfg := Config{
		EventStream: "IT_FEED_EVT_" + suffix, EventRoot: "evtit" + suffix,
		PublishStream: "IT_FEED_WS_" + suffix, PublishRoot: "wsit" + suffix + ".publish",
		Replicas: 1, Workers: 1, Queue: 1, ReplayRate: 20000, ConsumeBatch: 500, ConsumeBytes: 8 << 20,
	}
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: cfg.EventStream, Subjects: []string{cfg.EventRoot + ".>"}, Storage: jetstream.MemoryStorage}); err != nil {
		t.Fatalf("create event stream: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
		defer cancel()
		_ = js.DeleteStream(ctx, cfg.EventStream)
		_ = js.DeleteStream(ctx, cfg.PublishStream)
	})
	return js, cfg
}

func publishEvents(t *testing.T, js jetstream.JetStream, root string, from, to int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	for i := from; i <= to; i++ {
		subject := fmt.Sprintf("%s.acme.message.%d.msg_created", root, i%itRooms+1)
		if _, err := js.Publish(ctx, subject, []byte(strconv.Itoa(i)), jetstream.WithMsgID(fmt.Sprintf("it-%d", i))); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
}

func deleteConsumers(t *testing.T, js jetstream.JetStream, stream string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	s, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	names := s.ConsumerNames(ctx)
	deleted := 0
	for name := range names.Name() {
		if err := s.DeleteConsumer(ctx, name); err != nil {
			t.Fatalf("delete consumer %s: %v", name, err)
		}
		deleted++
	}
	if names.Err() != nil || deleted == 0 {
		t.Fatalf("deleted %d consumers, err %v; want the ordered consumer gone", deleted, names.Err())
	}
}

func TestFeedReadsEveryEventInRoomOrderAcrossAStallAndAConsumerLoss(t *testing.T) {
	url := os.Getenv("CHATIM_IT_NATS_URL")
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	js, cfg := itStreams(t, url)
	h := &itHandler{gate: make(chan struct{}), first: make(chan struct{}), all: make(chan struct{}), publish: make(chan Envelope, 1), rooms: map[string][]uint64{}}
	f, err := New(cfg, h, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	b, err := Dial(ctx, NATSConfig{URL: url, Name: "feed-it"}, quietLog())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := f.Start(ctx, b); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = f.Stop(context.Background())
		}
	}()

	publishEvents(t, js, cfg.EventRoot, 1, itEvents/2)
	waitFor(t, h.first, "first event")
	deleteConsumers(t, js, cfg.EventStream)
	publishEvents(t, js, cfg.EventRoot, itEvents/2+1, itEvents)
	close(h.gate)
	waitFor(t, h.all, "all 500 events")

	h.mu.Lock()
	total := 0
	for room, seqs := range h.rooms {
		total += len(seqs)
		for i := 1; i < len(seqs); i++ {
			if seqs[i] <= seqs[i-1] {
				t.Errorf("room %s out of order at %d: %v", room, i, seqs)
				break
			}
		}
	}
	h.mu.Unlock()
	if total != itEvents || len(h.rooms) != itRooms {
		t.Fatalf("received %d events in %d rooms, want %d in %d", total, len(h.rooms), itEvents, itRooms)
	}
	if f.streams[0].resets.Load() < 1 {
		t.Fatal("consumer_resets_total stayed 0 after the server lost the consumer")
	}
	if !f.Connected() {
		t.Fatal("feed reports NATS down")
	}

	subject := cfg.PublishRoot + ".acme"
	if _, err := js.Publish(ctx, subject, []byte("packet"), jetstream.WithMsgID("n1-p0-abcd1234")); err != nil {
		t.Fatalf("publish packet: %v", err)
	}
	select {
	case e := <-h.publish:
		if e.Tenant != "acme" || e.ID != "n1-p0-abcd1234" || string(e.Data) != "packet" || e.Seq != 1 {
			t.Fatalf("packet envelope = %+v", e)
		}
	case <-time.After(itTimeout):
		t.Fatal("ws publish packet not received")
	}
	stopped = true
	if err := f.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if f.Connected() {
		t.Fatal("NATS still connected after Stop")
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(itTimeout):
		t.Fatalf("%s did not arrive", what)
	}
}
