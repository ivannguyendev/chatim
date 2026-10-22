package app

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
)

type fakeNATS struct {
	release   chan struct{}
	dialed    chan struct{}
	dialErr   error
	connected atomic.Bool
	closed    atomic.Int32
	mu        sync.Mutex
	consumes  []*fakeConsumeContext
	streams   []string
}

func newFakeNATS() *fakeNATS {
	n := &fakeNATS{release: make(chan struct{}), dialed: make(chan struct{}, 1)}
	close(n.release)
	n.connected.Store(true)
	return n
}

func heldNATS() *fakeNATS {
	n := &fakeNATS{release: make(chan struct{}), dialed: make(chan struct{}, 1)}
	n.connected.Store(true)
	return n
}

func (n *fakeNATS) dial(ctx context.Context) (feed.Broker, error) {
	n.dialed <- struct{}{}
	select {
	case <-n.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if n.dialErr != nil {
		return nil, n.dialErr
	}
	return n, nil
}

func (n *fakeNATS) EnsureStream(_ context.Context, cfg jetstream.StreamConfig) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.streams = append(n.streams, cfg.Name)
	return nil
}

func (n *fakeNATS) LastSeq(context.Context, string) (uint64, error) { return 0, nil }

func (n *fakeNATS) OrderedConsumer(context.Context, string, jetstream.OrderedConsumerConfig) (feed.Consumer, error) {
	return n, nil
}

func (n *fakeNATS) Consume(jetstream.MessageHandler, ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	cc := &fakeConsumeContext{stopped: make(chan struct{})}
	n.consumes = append(n.consumes, cc)
	return cc, nil
}

func (n *fakeNATS) running() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	live := 0
	for _, cc := range n.consumes {
		select {
		case <-cc.stopped:
		default:
			live++
		}
	}
	return live
}

func (n *fakeNATS) Connected() bool    { return n.connected.Load() }
func (n *fakeNATS) Reconnects() uint64 { return 0 }
func (n *fakeNATS) Close()             { n.closed.Add(1) }

type fakeConsumeContext struct {
	stopped chan struct{}
	once    sync.Once
}

func (c *fakeConsumeContext) Stop()                   { c.once.Do(func() { close(c.stopped) }) }
func (c *fakeConsumeContext) Drain()                  { c.Stop() }
func (c *fakeConsumeContext) Closed() <-chan struct{} { return c.stopped }
