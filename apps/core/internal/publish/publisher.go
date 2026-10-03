package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var (
	ErrQueueFull = fmt.Errorf("publish queue full: %w", domain.ErrBusy)
	ErrClosed    = fmt.Errorf("publisher closed: %w", domain.ErrRetryLater)
	errStarted   = errors.New("publisher already started")
)

type JetStream interface {
	PublishMsgAsync(m *nats.Msg, opts ...jetstream.PublishOpt) (jetstream.PubAckFuture, error)
	PublishAsyncComplete() <-chan struct{}
}

type item struct {
	room   uint64
	events []*chatimv1.Event
}

type shard struct {
	queue chan item
	full  atomic.Bool
}

type Publisher struct {
	js      JetStream
	cfg     Config
	log     *slog.Logger
	fails   failureLog
	shards  []*shard
	mu      sync.RWMutex
	closed  bool
	started atomic.Bool
	abort   chan struct{}
	stop    sync.Once
	done    chan struct{}
}

func New(js JetStream, cfg Config, log *slog.Logger) (*Publisher, error) {
	if js == nil {
		return nil, fmt.Errorf("%w: publisher needs a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	p := &Publisher{js: js, cfg: cfg, log: log, fails: failureLog{log: log}, shards: make([]*shard, cfg.Shards), abort: make(chan struct{}), done: make(chan struct{})}
	for i := range p.shards {
		p.shards[i] = &shard{queue: make(chan item, cfg.QueueSize)}
	}
	return p, nil
}

func (p *Publisher) Run(ctx context.Context) error {
	if !p.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(p.done)
	var wg sync.WaitGroup
	for _, s := range p.shards {
		wg.Go(func() { p.drain(ctx, s.queue) })
	}
	wg.Wait()
	return ctx.Err()
}

func (p *Publisher) drain(ctx context.Context, queue <-chan item) {
	for {
		select {
		case it, open := <-queue:
			if !open {
				return
			}
			p.publish(it)
		case <-p.abort:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (p *Publisher) publish(it item) {
	for _, ev := range it.events {
		msg, err := message(p.cfg.SubjectRoot, it.room, ev)
		if err != nil {
			p.fails.record("dropping malformed event", ev.GetId(), err)
			continue
		}
		if _, err := p.js.PublishMsgAsync(msg, jetstream.WithRetryAttempts(p.cfg.Attempts), jetstream.WithRetryWait(p.cfg.RetryBackoff)); err != nil {
			p.fails.record("event publish refused; reconciliation must republish it", ev.GetId(), err)
		}
	}
}

func (p *Publisher) Enqueue(room uint64, events []*chatimv1.Event) error {
	if len(events) == 0 {
		return nil
	}
	s := p.shards[int(slotmap.Of(room))%len(p.shards)]
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	select {
	case s.queue <- item{room: room, events: events}:
		s.full.Store(false)
		return nil
	default:
		if s.full.CompareAndSwap(false, true) {
			p.log.Warn("publish queue full; dropping events", "room", room, "events", len(events))
		}
		return ErrQueueFull
	}
}

func (p *Publisher) Close(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		for _, s := range p.shards {
			close(s.queue)
		}
	}
	p.mu.Unlock()
	if !p.started.Load() {
		return nil
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		p.stop.Do(func() { close(p.abort) })
		<-p.done
		return ctx.Err()
	}
	select {
	case <-p.js.PublishAsyncComplete():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
