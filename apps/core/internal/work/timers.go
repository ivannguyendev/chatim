package work

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const disarmFailedMsg = "member count check timer not disarmed; it will fire and recount"

type Timer struct {
	Seq uint64
}

type TimerJetStream interface {
	PublishMsg(ctx context.Context, m *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
	Stream(ctx context.Context, name string) (jetstream.Stream, error)
}

type TimerOption func(*Timers)

func WithTimerLogger(log *slog.Logger) TimerOption { return func(t *Timers) { t.log = log } }

type Timers struct {
	js         TimerJetStream
	stream     string
	root       string
	partitions int
	delay      time.Duration
	log        *slog.Logger
	now        func() time.Time
	op         func() uint32
	mu         sync.Mutex
	handle     jetstream.Stream
}

func NewTimers(js TimerJetStream, streamName, subjectRoot string, partitions int, delay time.Duration, opts ...TimerOption) (*Timers, error) {
	switch {
	case js == nil:
		return nil, fmt.Errorf("%w: member count check timers need a jetstream client", apperr.ErrInvalidArgument)
	case !subjectToken(streamName) || !subjectToken(subjectRoot):
		return nil, fmt.Errorf("%w: timer stream %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, streamName, subjectRoot)
	case partitions <= 0 || partitions > slotmap.Count:
		return nil, fmt.Errorf("%w: timer partitions %d must be between 1 and the slot count %d", apperr.ErrInvalidArgument, partitions, slotmap.Count)
	case delay <= 0:
		return nil, fmt.Errorf("%w: timer delay %v must be positive", apperr.ErrInvalidArgument, delay)
	}
	t := &Timers{js: js, stream: streamName, root: subjectRoot, partitions: partitions, delay: delay, log: slog.Default(), now: time.Now, op: randomOp}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

func (t *Timers) Arm(ctx context.Context, room uint64) (Timer, error) {
	if room == 0 {
		return Timer{}, fmt.Errorf("%w: member count check of room 0", apperr.ErrInvalidArgument)
	}
	op, at := t.op(), fireAt(t.now(), t.delay)
	r := Record{Kind: store.MemberCountCheck, Room: room, Version: op, CommittedAt: at}
	m := &nats.Msg{Subject: t.subject(room, op), Data: Encode(r), Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, r.ID())
	ack, err := t.js.PublishMsg(ctx, m, jetstream.WithScheduleAt(at), jetstream.WithScheduleTarget(t.target(room)))
	if err != nil {
		return Timer{}, fmt.Errorf("arm member count check of room %d: %w", room, err)
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

func (t *Timers) subject(room uint64, op uint32) string {
	return t.root + ".timer." + pbconv.RoomID(room) + "." + strconv.FormatUint(uint64(op), 10)
}

func (t *Timers) target(room uint64) string { return Subject(t.root, Partition(room, t.partitions)) }

func fireAt(now time.Time, delay time.Duration) time.Time {
	at := now.Add(delay).UTC().Round(0)
	if whole := at.Truncate(time.Second); whole.Before(at) {
		return whole.Add(time.Second)
	}
	return at
}

func randomOp() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}
