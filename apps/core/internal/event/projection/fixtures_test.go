package projection_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	roomA  uint64 = 7_350_000_001
	roomB  uint64 = 7_350_000_002
	roomC  uint64 = 7_350_000_003
	tenant        = "acme"
)

var baseTime = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return baseTime.Add(time.Duration(sec) * time.Second) }

func envelope(room, thread, seq uint64, ts time.Time) *chatimv1.Event {
	return &chatimv1.Event{
		Id: strconv.FormatUint(room, 10) + "-" + strconv.FormatUint(seq, 10), Tenant: tenant,
		RoomId: strconv.FormatUint(room, 10), ThreadRoot: thread, Seq: seq, Ts: timestamppb.New(ts),
	}
}

func msgCreated(room, thread, seq uint64, ts time.Time) *chatimv1.Event {
	ev := envelope(room, thread, seq, ts)
	ev.Payload = &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{Seq: seq}}}
	return ev
}

func msgEdited(room, thread, seq uint64, ts time.Time) *chatimv1.Event {
	ev := envelope(room, thread, seq, ts)
	ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Message: &chatimv1.Message{Seq: seq}, Ver: 2}}
	return ev
}

func reactionChanged(room, seq uint64, ts time.Time) *chatimv1.Event {
	ev := envelope(room, 0, seq, ts)
	ev.Payload = &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{User: "u1", Emoji: "👍", Change: 1}}
	return ev
}

func readUpdated(room uint64, ts time.Time) *chatimv1.Event {
	ev := envelope(room, 0, 0, ts)
	ev.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: "u1", ReadSeq: 3, ReadVer: 2}}
	return ev
}

func encoded(t *testing.T, ev *chatimv1.Event) []byte {
	t.Helper()
	b, err := proto.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return b
}

type fakeWriter struct {
	mu    sync.Mutex
	calls [][]store.Activity
	err   error
	hold  chan struct{}
	held  chan struct{}
}

func (w *fakeWriter) TouchActivity(ctx context.Context, acts []store.Activity) error {
	w.mu.Lock()
	w.calls = append(w.calls, slices.Clone(acts))
	err, hold, held := w.err, w.hold, w.held
	w.hold = nil
	w.mu.Unlock()
	if hold != nil {
		close(held)
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (w *fakeWriter) touched() [][]store.Activity {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.calls)
}

func (w *fakeWriter) holdNext() (held, release chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hold, w.held = make(chan struct{}), make(chan struct{})
	return w.held, w.hold
}

func built[T any](v T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}

func decoded(t *testing.T, ev *chatimv1.Event) projection.Event {
	t.Helper()
	d, err := projection.Decode(encoded(t, ev))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return d
}
