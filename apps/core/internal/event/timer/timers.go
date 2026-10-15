package timer

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const disarmFailedMsg = "count check timer not disarmed; it will fire and recount"

type Timer struct {
	Seq uint64
}

type JetStream interface {
	PublishMsg(ctx context.Context, m *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
	Stream(ctx context.Context, name string) (jetstream.Stream, error)
}

type Option func(*Timers)

func WithLogger(log *slog.Logger) Option { return func(t *Timers) { t.log = log } }

type Timers struct {
	js     JetStream
	stream string
	root   string
	delay  time.Duration
	log    *slog.Logger
	now    func() time.Time
	op     func() uint32
	mu     sync.Mutex
	handle jetstream.Stream
}

func NewTimers(js JetStream, streamName, subjectRoot string, delay time.Duration, opts ...Option) (*Timers, error) {
	switch {
	case js == nil:
		return nil, fmt.Errorf("%w: count check timers need a jetstream client", apperr.ErrInvalidArgument)
	case !subjectToken(streamName) || !subjectToken(subjectRoot):
		return nil, fmt.Errorf("%w: timer stream %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, streamName, subjectRoot)
	case delay <= 0:
		return nil, fmt.Errorf("%w: timer delay %v must be positive", apperr.ErrInvalidArgument, delay)
	}
	t := &Timers{js: js, stream: streamName, root: subjectRoot, delay: delay, log: slog.Default(), now: time.Now, op: RandomOp}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

func (t *Timers) ArmMemberCountCheck(ctx context.Context, room uint64) (Timer, error) {
	return t.arm(ctx, Check{Room: room, Counter: MembersCounter, Op: t.op()})
}

func (t *Timers) ArmMessageCountCheck(ctx context.Context, key store.MsgKey, counter string) (Timer, error) {
	return t.arm(ctx, Check{Room: key.Room, Thread: key.Thread, Seq: key.Seq, Counter: counter, Op: t.op()})
}

func (t *Timers) arm(ctx context.Context, c Check) (Timer, error) {
	if err := c.Validate(); err != nil {
		return Timer{}, err
	}
	m := &nats.Msg{Subject: t.root + ".arm." + c.subjectTail(), Data: Encode(c), Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, c.ID())
	ack, err := t.js.PublishMsg(ctx, m, jetstream.WithScheduleAt(fireAt(t.now(), t.delay)), jetstream.WithScheduleTarget(DueSubject(t.root)))
	if err != nil {
		return Timer{}, fmt.Errorf("arm count check %s: %w", c.ID(), err)
	}
	return Timer{Seq: ack.Sequence}, nil
}

func (t *Timers) Disarm(ctx context.Context, tm Timer) {
	if tm.Seq == 0 {
		return
	}
	s, err := t.streamHandle(ctx)
	if err == nil {
		err = s.DeleteMsg(ctx, tm.Seq)
	}
	if err != nil && !errors.Is(err, jetstream.ErrMsgNotFound) {
		t.log.WarnContext(ctx, disarmFailedMsg, "stream", t.stream, "seq", tm.Seq, "err", err)
	}
}

func (t *Timers) streamHandle(ctx context.Context) (jetstream.Stream, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.handle == nil {
		s, err := t.js.Stream(ctx, t.stream)
		if err != nil {
			return nil, err
		}
		t.handle = s
	}
	return t.handle, nil
}

func RandomOp() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

func fireAt(now time.Time, delay time.Duration) time.Time {
	at := now.Add(delay).UTC().Round(0)
	if whole := at.Truncate(time.Second); whole.Before(at) {
		return whole.Add(time.Second)
	}
	return at
}
