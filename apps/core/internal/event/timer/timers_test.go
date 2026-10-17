package timer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
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
	return &jetstream.PubAck{Stream: "CHATIM_TIMER", Sequence: uint64(40 + len(f.msgs))}, nil
}

func (f *fakeTimerJS) Stream(_ context.Context, name string) (jetstream.Stream, error) {
	f.streamCalls++
	if f.streamErr != nil || name != "CHATIM_TIMER" {
		return nil, errors.Join(f.streamErr, errors.New("unknown stream "+name))
	}
	return f.stream, nil
}

func newTestTimers(t *testing.T, js JetStream, sink *testlog.Sink) *Timers {
	t.Helper()
	tm, err := NewTimers(js, "CHATIM_TIMER", "timer", 5*time.Second, WithLogger(sink.Logger()))
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
		"no client":     func() (*Timers, error) { return NewTimers(nil, "T", "timer", time.Second) },
		"dotted stream": func() (*Timers, error) { return NewTimers(js, "T.X", "timer", time.Second) },
		"wildcard root": func() (*Timers, error) { return NewTimers(js, "T", "timer>", time.Second) },
		"zero delay":    func() (*Timers, error) { return NewTimers(js, "T", "timer", 0) },
	}
	for name, build := range cases {
		if tm, err := build(); !errors.Is(err, apperr.ErrInvalidArgument) || tm != nil {
			t.Errorf("%s: NewTimers = %v, %v; want ErrInvalidArgument", name, tm, err)
		}
	}
}

func TestArmMemberCountCheckSchedulesTheCheckOnTheArmSubject(t *testing.T) {
	js := &fakeTimerJS{}
	tm := newTestTimers(t, js, &testlog.Sink{})
	armed, err := tm.ArmMemberCountCheck(t.Context(), 777)
	if err != nil || armed != (Timer{Seq: 41}) {
		t.Fatalf("ArmMemberCountCheck = %+v, %v; want the PubAck sequence 41", armed, err)
	}
	if len(js.msgs) != 1 || js.msgs[0].Subject != "timer.arm.777.42" || js.optCounts[0] != 2 {
		t.Fatalf("published %d msgs, first on %q with %v options; want one on timer.arm.777.42 with the schedule time and target", len(js.msgs), js.msgs[0].Subject, js.optCounts)
	}
	c, err := Decode(js.msgs[0].Data)
	if want := (Check{Room: 777, Counter: MembersCounter, Op: 42}); err != nil || c != want {
		t.Fatalf("payload = %+v, %v; want %+v", c, err, want)
	}
	if id := js.msgs[0].Header.Get(jetstream.MsgIDHeader); id != "k:777-42" {
		t.Fatalf("Nats-Msg-Id = %q, want k:777-42", id)
	}
}

func TestArmMessageCountCheckSchedulesTheCheckOnTheMessageSubject(t *testing.T) {
	js := &fakeTimerJS{}
	tm := newTestTimers(t, js, &testlog.Sink{})
	if _, err := tm.ArmMessageCountCheck(t.Context(), store.MsgKey{Room: 777, Seq: 9}, pbconv.RepliesCounter); err != nil {
		t.Fatalf("ArmMessageCountCheck: %v", err)
	}
	if len(js.msgs) != 1 || js.msgs[0].Subject != "timer.arm.777.0.9.replies.42" {
		t.Fatalf("published %d msgs; want one on timer.arm.777.0.9.replies.42", len(js.msgs))
	}
	if id := js.msgs[0].Header.Get(jetstream.MsgIDHeader); id != "q:777-0-9-replies-42" {
		t.Fatalf("Nats-Msg-Id = %q", id)
	}
	bad := []func() error{
		func() error {
			_, err := tm.ArmMessageCountCheck(t.Context(), store.MsgKey{Room: 777, Thread: 3, Seq: 9}, pbconv.RepliesCounter)
			return err
		},
		func() error {
			_, err := tm.ArmMessageCountCheck(t.Context(), store.MsgKey{Room: 777}, pbconv.RepliesCounter)
			return err
		},
		func() error {
			_, err := tm.ArmMessageCountCheck(t.Context(), store.MsgKey{Room: 777, Seq: 9}, "pins")
			return err
		},
		func() error { _, err := tm.ArmMemberCountCheck(t.Context(), 0); return err },
	}
	for i, arm := range bad {
		if err := arm(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("bad arm %d = %v, want ErrInvalidArgument", i, err)
		}
	}
	if len(js.msgs) != 1 {
		t.Fatalf("published %d msgs, want no publish for refused checks", len(js.msgs))
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

func TestArmFailsWithoutATimer(t *testing.T) {
	tm := newTestTimers(t, &fakeTimerJS{pubErr: errNATS}, &testlog.Sink{})
	if armed, err := tm.ArmMemberCountCheck(t.Context(), 777); !errors.Is(err, errNATS) || armed != (Timer{}) {
		t.Fatalf("ArmMemberCountCheck with NATS down = %+v, %v; want the publish error", armed, err)
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
	js.stream.err = jetstream.ErrMsgNotFound
	tm.Disarm(t.Context(), Timer{Seq: 46})
	js.stream.err = errNATS
	tm.Disarm(t.Context(), Timer{Seq: 44})
	failing := newTestTimers(t, &fakeTimerJS{streamErr: errNATS}, sink)
	failing.Disarm(t.Context(), Timer{Seq: 45})
	if got := sink.Count(disarmFailedMsg); got != 2 {
		t.Fatalf("%q logged %d times, want 2", disarmFailedMsg, got)
	}
}

func TestRandomOpsDiffer(t *testing.T) {
	seen := map[uint32]bool{}
	for range 8 {
		seen[RandomOp()] = true
	}
	if len(seen) < 7 {
		t.Fatalf("8 random ops gave %d distinct values", len(seen))
	}
}
