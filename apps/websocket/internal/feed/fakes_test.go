package feed

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type fakeMsg struct {
	jetstream.Msg
	subject string
	data    []byte
	header  nats.Header
	meta    *jetstream.MsgMetadata
	metaErr error
}

func (m *fakeMsg) Subject() string      { return m.subject }
func (m *fakeMsg) Data() []byte         { return m.data }
func (m *fakeMsg) Headers() nats.Header { return m.header }

func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) { return m.meta, m.metaErr }

func eventMsg(subject string, seq uint64, stored time.Time) *fakeMsg {
	return &fakeMsg{
		subject: subject,
		data:    []byte{byte(seq)},
		header:  nats.Header{jetstream.MsgIDHeader: []string{subject}},
		meta: &jetstream.MsgMetadata{
			Sequence:  jetstream.SequencePair{Stream: seq, Consumer: seq},
			Timestamp: stored,
			Consumer:  "Abc123_1",
		},
	}
}

type fakeConsumeContext struct {
	stopped chan struct{}
	once    sync.Once
}

func (c *fakeConsumeContext) Stop()                   { c.once.Do(func() { close(c.stopped) }) }
func (c *fakeConsumeContext) Drain()                  { c.Stop() }
func (c *fakeConsumeContext) Closed() <-chan struct{} { return c.stopped }

type fakeConsumer struct {
	stream  string
	cfg     jetstream.OrderedConsumerConfig
	handler jetstream.MessageHandler
	opts    int
	ctx     *fakeConsumeContext
	refuse  error
}

func (c *fakeConsumer) Consume(h jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error) {
	if c.refuse != nil {
		return nil, c.refuse
	}
	c.handler, c.opts = h, len(opts)
	c.ctx = &fakeConsumeContext{stopped: make(chan struct{})}
	return c.ctx, nil
}

type fakeBroker struct {
	mu          sync.Mutex
	ensured     []jetstream.StreamConfig
	consumers   map[string]*fakeConsumer
	history     map[string][]*fakeConsumer
	lastSeq     map[string]uint64
	created     map[string]time.Time
	positionErr map[string]error
	onPosition  func(stream string)
	ensureErr   error
	consumerErr map[string]error
	consumeErr  map[string]error
	connected   atomic.Bool
	reconnects  atomic.Uint64
	closed      atomic.Int32
}

func newFakeBroker() *fakeBroker {
	b := &fakeBroker{
		consumers: map[string]*fakeConsumer{}, history: map[string][]*fakeConsumer{}, lastSeq: map[string]uint64{}, created: map[string]time.Time{},
		positionErr: map[string]error{}, consumerErr: map[string]error{}, consumeErr: map[string]error{},
	}
	b.connected.Store(true)
	return b
}

func (b *fakeBroker) EnsureStream(_ context.Context, cfg jetstream.StreamConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensured = append(b.ensured, cfg)
	return b.ensureErr
}

func (b *fakeBroker) Position(_ context.Context, stream string) (Position, error) {
	b.mu.Lock()
	err, pos, hook := b.positionErr[stream], Position{Last: b.lastSeq[stream], Created: b.created[stream]}, b.onPosition
	b.mu.Unlock()
	if err != nil {
		return Position{}, err
	}
	if hook != nil {
		hook(stream)
	}
	return pos, nil
}

func (b *fakeBroker) set(f func(*fakeBroker)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(b)
}

func (b *fakeBroker) consumersOf(stream string) []*fakeConsumer {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*fakeConsumer(nil), b.history[stream]...)
}

func (b *fakeBroker) OrderedConsumer(_ context.Context, stream string, cfg jetstream.OrderedConsumerConfig) (Consumer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.consumerErr[stream]; err != nil {
		return nil, err
	}
	c := &fakeConsumer{stream: stream, cfg: cfg, refuse: b.consumeErr[stream]}
	b.consumers[stream] = c
	b.history[stream] = append(b.history[stream], c)
	return c, nil
}

func (b *fakeBroker) consumer(stream string) *fakeConsumer {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consumers[stream]
}

func (b *fakeBroker) Connected() bool    { return b.connected.Load() }
func (b *fakeBroker) Reconnects() uint64 { return b.reconnects.Load() }
func (b *fakeBroker) Close()             { b.closed.Add(1) }

var errBoom = errors.New("boom")
