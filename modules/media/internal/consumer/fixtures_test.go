package consumer_test

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/consumer"
	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const tenant = "acme"

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func cfg() consumer.Config {
	return consumer.Config{Stream: "CHATIM_EVT", SubjectRoot: "evt", FetchBatch: 8, Workers: 4, AckWait: 30 * time.Second, SyncTimeout: 15 * time.Minute}
}

func envelope(seq uint64, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{Tenant: tenant, RoomId: "777", Seq: seq, Ts: timestamppb.New(at)}
}

func created(seq uint64, at time.Time, files int) *chatimv1.Event {
	ev := envelope(seq, at)
	msg := &chatimv1.Message{RoomId: "777", Seq: seq}
	for range files {
		msg.Attachments = append(msg.Attachments, &chatimv1.Attachment{Key: "acme/an/20261108/0123456789abcdefghijklmnop"})
	}
	ev.Payload = &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: msg}}
	return ev
}

func deleted(seq uint64, at time.Time) *chatimv1.Event {
	ev := envelope(seq, at)
	ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Message: &chatimv1.Message{RoomId: "777", Seq: seq, Deleted: true}}}
	return ev
}

type syncCall struct {
	kind attach.Kind
	ev   attach.Event
}

type fakeSync struct {
	mu    sync.Mutex
	calls []syncCall
	err   error
	block chan struct{}
	delay time.Duration
	busy  int
}

func (s *fakeSync) Sync(ctx context.Context, kind attach.Kind, ev attach.Event) error {
	s.mu.Lock()
	s.calls = append(s.calls, syncCall{kind, ev})
	s.busy++
	block, delay, err := s.block, s.delay, s.err
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy--
		s.mu.Unlock()
	}()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if delay > 0 {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (s *fakeSync) recorded() []syncCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *fakeSync) inFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

type rig struct {
	sync  *fakeSync
	stats *attach.Stats
	ready func() bool
}

func newRig() *rig {
	return &rig{sync: &fakeSync{}, stats: &attach.Stats{}, ready: func() bool { return true }}
}

func (r *rig) deps(src jsconsume.Source) consumer.Deps {
	return consumer.Deps{Source: src, Sync: r.sync, Stats: r.stats, Ready: r.ready, Log: slog.New(slog.DiscardHandler)}
}

func (r *rig) consumer(t *testing.T, src jsconsume.Source, c consumer.Config) *consumer.Consumer {
	t.Helper()
	out, err := consumer.New(c, r.deps(src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return out
}

type running struct {
	c    *consumer.Consumer
	done chan error
}

func start(ctx context.Context, c *consumer.Consumer) running {
	r := running{c: c, done: make(chan error, 1)}
	go func() { r.done <- c.Run(ctx) }()
	return r
}

func (r running) stop(t *testing.T) {
	t.Helper()
	if err := r.c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-r.done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func runOnce(t *testing.T, r *rig, c consumer.Config, ds ...*fakeDelivery) *consumer.Consumer {
	t.Helper()
	q := &fakeQueue{}
	q.push(ds...)
	cons := r.consumer(t, q, c)
	run := start(t.Context(), cons)
	synctest.Wait()
	run.stop(t)
	return cons
}

func delivery(t *testing.T, ev *chatimv1.Event, delivered uint64) *fakeDelivery {
	t.Helper()
	data, err := proto.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &fakeDelivery{data: data, delivered: delivered}
}
