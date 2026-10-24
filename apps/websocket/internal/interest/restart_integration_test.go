package interest

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itTimeout  = 30 * time.Second
	itMaxAge   = 2 * time.Second
	itStallFor = 3 * time.Second
	itRoom     = 1
	itFillers  = 20
)

type itSink struct {
	mu     sync.Mutex
	frames []string
	signal chan struct{}
}

func newITSink() *itSink { return &itSink{signal: make(chan struct{}, 1)} }

func (s *itSink) Send(raw []byte) {
	line := describe(raw)
	s.mu.Lock()
	s.frames = append(s.frames, line)
	s.mu.Unlock()
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *itSink) TrySend(raw []byte) bool {
	s.Send(raw)
	return true
}

func (s *itSink) Offer(raw []byte) bool {
	return s.TrySend(raw)
}

func (s *itSink) Fail() {}

func (s *itSink) since(from int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.frames[from:])
}

func (s *itSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func (s *itSink) await(t *testing.T, line string) {
	t.Helper()
	deadline := time.After(itTimeout)
	for {
		if slices.Contains(s.since(0), line) {
			return
		}
		select {
		case <-s.signal:
		case <-deadline:
			t.Fatalf("frame %q did not arrive; frames %v", line, s.since(0))
		}
	}
}

type itJump struct {
	jump   feed.Jump
	counts [2]int
}

type itRouter struct {
	table  *Table
	sinks  [2]*itSink
	gate   chan struct{}
	first  chan struct{}
	once   sync.Once
	jumped chan itJump
}

func (r *itRouter) Event(env feed.Envelope) {
	r.once.Do(func() { close(r.first) })
	<-r.gate
	r.table.Route(env)
}

func (r *itRouter) Publish(feed.Envelope) {}

func (r *itRouter) Jumped(j feed.Jump) {
	counts := [2]int{r.sinks[0].count(), r.sinks[1].count()}
	r.table.Jumped(j)
	r.jumped <- itJump{jump: j, counts: counts}
}

func publishEvent(t *testing.T, js jetstream.JetStream, root string, ev *chatimv1.Event, data, kind string) {
	t.Helper()
	raw, err := proto.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	subject := fmt.Sprintf("%s.%s.%s.%s.%s", root, ev.GetTenant(), data, ev.GetRoomId(), kind)
	if _, err := js.Publish(ctx, subject, raw, jetstream.WithMsgID(ev.GetId())); err != nil {
		t.Fatalf("publish %s: %v", ev.GetId(), err)
	}
}

func roomMessage(id string) *chatimv1.Event {
	return &chatimv1.Event{Id: id, Tenant: "t1", RoomId: strconv.Itoa(itRoom)}
}

func itEventStream(t *testing.T, url string) (jetstream.JetStream, feed.Config) {
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
	cfg := feed.Config{
		EventStream: "IT_RESTART_EVT_" + suffix, EventRoot: "evtrs" + suffix,
		PublishStream: "IT_RESTART_WS_" + suffix, PublishRoot: "wsrs" + suffix + ".publish",
		Replicas: 1, Workers: 1, Queue: 1, ReplayRate: 20000, ConsumeBatch: 500, ConsumeBytes: 8 << 20,
		ReplayMaxAge: itMaxAge, JumpCooldown: itMaxAge,
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

func TestALongStallSuspendsUsersAndAMemberRemovedMeanwhileEndsTheRoomForThem(t *testing.T) {
	url := os.Getenv("CHATIM_IT_NATS_URL")
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	js, cfg := itEventStream(t, url)
	loader := newFakeLoader()
	loader.set("t1", "stays", itRoom)
	loader.set("t1", "removed", itRoom)
	table, err := New(Config{Inflight: 4, OpenPending: 10 * time.Second, ReloadRate: 1000, UserConnectionsMax: 10}, loader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	stays, removed := newITSink(), newITSink()
	defer mustRegister(t, table, "t1", "stays", stays).Unregister()
	defer mustRegister(t, table, "t1", "removed", removed).Unregister()
	stays.await(t, "ready")
	removed.await(t, "ready")

	r := &itRouter{table: table, sinks: [2]*itSink{stays, removed}, gate: make(chan struct{}), first: make(chan struct{}), jumped: make(chan itJump, 4)}
	f, err := feed.New(cfg, r, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	b, err := feed.Dial(ctx, feed.NATSConfig{URL: url, Name: "interest-restart-it"}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := f.Start(ctx, b); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = f.Stop(context.Background()) }()

	publishEvent(t, js, cfg.EventRoot, roomMessage("before"), "message", "msg_created")
	select {
	case <-r.first:
	case <-time.After(itTimeout):
		t.Fatal("the first event did not arrive")
	}
	removal := roomMessage("removal")
	removal.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{User: "removed", Ver: 2}}
	publishEvent(t, js, cfg.EventRoot, removal, "member", "member_removed")
	for i := range itFillers {
		publishEvent(t, js, cfg.EventRoot, roomMessage(fmt.Sprintf("stalled-%d", i)), "message", "msg_created")
	}
	loader.set("t1", "removed")
	loader.setLeft("t1", "removed", Room{ID: itRoom, Ver: 2})
	time.Sleep(itStallFor)
	close(r.gate)

	var j itJump
	select {
	case j = <-r.jumped:
	case <-time.After(itTimeout):
		t.Fatal("the feed did not skip after the stall")
	}
	if j.jump.Reason != feed.JumpLagTooOld {
		t.Fatalf("jump = %+v", j.jump)
	}
	stays.await(t, "resync lag_too_old")
	removed.await(t, "resync lag_too_old")
	publishEvent(t, js, cfg.EventRoot, roomMessage("after"), "message", "msg_created")
	stays.await(t, "event after")

	if got := stays.since(j.counts[0]); !slices.Equal(got, []string{"resync lag_too_old", "event after"}) {
		t.Fatalf("staying user frames after the jump = %v", got)
	}
	if got := removed.since(j.counts[1]); !slices.Equal(got, []string{"resync lag_too_old"}) {
		t.Fatalf("removed user frames after the jump = %v", got)
	}
	if table.stats.suspended.Load() != 0 || table.stats.reloads.Load() != 2 {
		t.Fatalf("suspended %d reloads %d", table.stats.suspended.Load(), table.stats.reloads.Load())
	}
}
