package work

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errNATS = errors.New("nats down")

type fakeStream struct {
	jetstream.Stream
	deleted []uint64
	err     error
}

func (s *fakeStream) DeleteMsg(_ context.Context, seq uint64) error {
	s.deleted = append(s.deleted, seq)
	return s.err
}

type fakeTimerJS struct {
	msgs        []*nats.Msg
	optCounts   []int
	pubErr      error
	stream      *fakeStream
	streamErr   error
	streamCalls int
}

func (f *fakeTimerJS) PublishMsg(_ context.Context, m *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	f.msgs, f.optCounts = append(f.msgs, m), append(f.optCounts, len(opts))
	if f.pubErr != nil {
		return nil, f.pubErr
	}
	return &jetstream.PubAck{Stream: "CHATIM_WORK", Sequence: uint64(40 + len(f.msgs))}, nil
}

func (f *fakeTimerJS) Stream(_ context.Context, name string) (jetstream.Stream, error) {
	f.streamCalls++
	if f.streamErr != nil || name != "CHATIM_WORK" {
		return nil, errors.Join(f.streamErr, errors.New("unknown stream "+name))
	}
	return f.stream, nil
}

func newTestTimers(t *testing.T, js TimerJetStream, sink *testlog.Sink) *Timers {
	t.Helper()
	tm, err := NewTimers(js, "CHATIM_WORK", "work", 32, 5*time.Second, WithTimerLogger(sink.Logger()))
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	tm.now = func() time.Time { return time.Date(2026, 10, 8, 10, 0, 0, 250_000_000, time.UTC) }
	tm.op = func() uint32 { return 42 }
	return tm
}

func TestNewTimersRejectsBadConfig(t *testing.T) {
	js := &fakeTimerJS{}
	cases := map[string]func() (*Timers, error){
		"no client":         func() (*Timers, error) { return NewTimers(nil, "W", "work", 32, time.Second) },
		"dotted stream":     func() (*Timers, error) { return NewTimers(js, "W.X", "work", 32, time.Second) },
		"wildcard root":     func() (*Timers, error) { return NewTimers(js, "W", "work>", 32, time.Second) },
		"zero partitions":   func() (*Timers, error) { return NewTimers(js, "W", "work", 0, time.Second) },
		"partitions > slot": func() (*Timers, error) { return NewTimers(js, "W", "work", 1025, time.Second) },
		"zero delay":        func() (*Timers, error) { return NewTimers(js, "W", "work", 32, 0) },
	}
	for name, build := range cases {
		if tm, err := build(); !errors.Is(err, apperr.ErrInvalidArgument) || tm != nil {
			t.Errorf("%s: NewTimers = %v, %v; want ErrInvalidArgument", name, tm, err)
		}
	}
}

func TestArmPublishesACountCheckOnTheTimerSubjectAndKeepsItsSequence(t *testing.T) {
	js := &fakeTimerJS{}
	tm := newTestTimers(t, js, &testlog.Sink{})
	timer, err := tm.Arm(t.Context(), 777)
	if err != nil || timer != (Timer{Seq: 41}) {
		t.Fatalf("Arm = %+v, %v; want the PubAck sequence 41", timer, err)
	}
	if len(js.msgs) != 1 || js.msgs[0].Subject != "work.timer.777.42" || js.optCounts[0] != 2 {
		t.Fatalf("published %d msgs, first on %q with %v options; want one on work.timer.777.42 with the schedule time and target", len(js.msgs), js.msgs[0].Subject, js.optCounts)
	}
	fire := time.Date(2026, 10, 8, 10, 0, 6, 0, time.UTC)
	r, err := Decode(js.msgs[0].Data)
	if want := (Record{Kind: store.MemberCountCheck, Room: 777, Version: 42, CommittedAt: fire}); err != nil || r != want {
		t.Fatalf("payload = %+v, %v; want %+v", r, err, want)
	}
	if id := js.msgs[0].Header.Get(jetstream.MsgIDHeader); id != "k:777-42" {
		t.Fatalf("Nats-Msg-Id = %q, want k:777-42", id)
	}
	if got := tm.target(777); got != Subject("work", Partition(777, 32)) {
		t.Fatalf("target = %q, want the room partition subject", got)
	}
}

func TestFireTimeRoundsUpToAWholeSecond(t *testing.T) {
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		now  time.Time
		want time.Time
	}{
		{base, base.Add(5 * time.Second)},
		{base.Add(time.Nanosecond), base.Add(6 * time.Second)},
		{base.Add(999 * time.Millisecond), base.Add(6 * time.Second)},
	} {
		if got := fireAt(c.now, 5*time.Second); !got.Equal(c.want) || got.Location() != time.UTC {
			t.Errorf("fireAt(%v, 5s) = %v, want %v", c.now, got, c.want)
		}
	}
}

func TestArmFailsWithoutWritingAnything(t *testing.T) {
	js := &fakeTimerJS{pubErr: errNATS}
	tm := newTestTimers(t, js, &testlog.Sink{})
	if _, err := tm.Arm(t.Context(), 777); !errors.Is(err, errNATS) {
		t.Fatalf("Arm with NATS down = %v, want the publish error", err)
	}
	if _, err := tm.Arm(t.Context(), 0); !errors.Is(err, apperr.ErrInvalidArgument) || len(js.msgs) != 1 {
		t.Fatalf("Arm(room 0) = %v after %d publishes, want ErrInvalidArgument and no publish", err, len(js.msgs))
	}
}

func TestDisarmDeletesTheTimerAndOnlyLogsFailures(t *testing.T) {
	js := &fakeTimerJS{stream: &fakeStream{}}
	sink := &testlog.Sink{}
	tm := newTestTimers(t, js, sink)
	tm.Disarm(t.Context(), Timer{Seq: 41})
	tm.Disarm(t.Context(), Timer{Seq: 43})
	tm.Disarm(t.Context(), Timer{})
	if len(js.stream.deleted) != 2 || js.stream.deleted[0] != 41 || js.stream.deleted[1] != 43 || js.streamCalls != 1 {
		t.Fatalf("deleted %v with %d stream lookups, want 41 and 43 with one lookup", js.stream.deleted, js.streamCalls)
	}
	js.stream.err = errNATS
	tm.Disarm(t.Context(), Timer{Seq: 44})
	failing := newTestTimers(t, &fakeTimerJS{streamErr: errNATS}, sink)
	failing.Disarm(t.Context(), Timer{Seq: 45})
	if got := sink.Count(disarmFailedMsg); got != 2 {
		t.Fatalf("%q logged %d times, want 2", disarmFailedMsg, got)
	}
}

func TestTimerOpsAreRandom(t *testing.T) {
	if a, b, c := randomOp(), randomOp(), randomOp(); a == b && b == c {
		t.Fatalf("randomOp returned %d three times", a)
	}
}
