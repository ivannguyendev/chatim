package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

const inactiveThreshold = 30 * time.Second

var (
	errNoHandler    = errors.New("feed needs a handler")
	errConsumeStuck = errors.New("consumer did not stop in time")
	errStartedTwice = errors.New("feed already started")
)

type Config struct {
	EventStream   string
	EventRoot     string
	PublishStream string
	PublishRoot   string
	Replicas      int
	Workers       int
	Queue         int
	ReplayRate    int
	ConsumeBatch  int
	ConsumeBytes  int
}

type Feed struct {
	cfg      Config
	handler  Handler
	log      *slog.Logger
	streams  [2]*streamStats
	unknown  atomic.Uint64
	dropped  atomic.Uint64
	broker   atomic.Pointer[Broker]
	fan      atomic.Pointer[fanout]
	mu       sync.Mutex
	started  bool
	cancel   context.CancelFunc
	consumes []jetstream.ConsumeContext
}

func New(cfg Config, h Handler, log *slog.Logger) (*Feed, error) {
	if h == nil {
		return nil, errNoHandler
	}
	return &Feed{
		cfg:     cfg,
		handler: h,
		log:     log,
		streams: [2]*streamStats{{stream: cfg.EventStream}, {stream: cfg.PublishStream}},
	}, nil
}

func OrderedConfig(filter string, startSeq uint64) jetstream.OrderedConsumerConfig {
	return jetstream.OrderedConsumerConfig{
		FilterSubjects:    []string{filter},
		DeliverPolicy:     jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:       startSeq,
		InactiveThreshold: inactiveThreshold,
	}
}

func (f *Feed) Start(ctx context.Context, b Broker) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started {
		return errStartedTwice
	}
	f.started = true
	f.broker.Store(&b)
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f.cancel = cancel
	fan := newFanout(f.cfg.Workers, f.cfg.Queue, f.handler, f.log)
	f.fan.Store(fan)
	err := f.open(ctx, life, b, fan)
	if err != nil {
		return errors.Join(err, f.stopLocked(ctx))
	}
	return nil
}

func (f *Feed) open(ctx, life context.Context, b Broker, fan *fanout) error {
	cfg := wspublish.StreamConfig(f.cfg.PublishStream, f.cfg.PublishRoot, f.cfg.Replicas, wspublish.DefaultMaxAge, wspublish.DefaultDuplicates)
	if err := b.EnsureStream(ctx, cfg); err != nil {
		return err
	}
	sources := []struct {
		stats   *streamStats
		root    string
		publish bool
	}{
		{f.streams[0], f.cfg.EventRoot, false},
		{f.streams[1], f.cfg.PublishRoot, true},
	}
	for _, s := range sources {
		r := newReceiver(life, s.root, s.publish, fan, f.cfg.ReplayRate, s.stats, &f.unknown, &f.dropped)
		if err := f.consume(ctx, b, r); err != nil {
			return err
		}
	}
	return nil
}

func (f *Feed) consume(ctx context.Context, b Broker, r *receiver) error {
	stream := r.stats.stream
	last, err := b.LastSeq(ctx, stream)
	if err != nil {
		return err
	}
	c, err := b.OrderedConsumer(ctx, stream, OrderedConfig(r.root+".>", last+1))
	if err != nil {
		return err
	}
	cc, err := c.Consume(r.handle,
		jetstream.PullMaxMessagesWithBytesLimit(f.cfg.ConsumeBatch, f.cfg.ConsumeBytes),
		f.consumeErrors(stream))
	if err != nil {
		return fmt.Errorf("consume %s: %w", stream, err)
	}
	f.consumes = append(f.consumes, cc)
	return nil
}

func (f *Feed) Stop(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.started {
		return nil
	}
	return f.stopLocked(ctx)
}

func (f *Feed) stopLocked(ctx context.Context) error {
	if f.cancel == nil {
		return nil
	}
	f.cancel()
	f.cancel = nil
	fan := f.fan.Load()
	fan.halt()
	var errs []error
	for _, cc := range f.consumes {
		cc.Stop()
	}
	for _, cc := range f.consumes {
		if !closedWithin(ctx, cc) {
			errs = append(errs, errConsumeStuck)
			break
		}
	}
	f.consumes = nil
	errs = append(errs, fan.close(ctx))
	(*f.broker.Load()).Close()
	return errors.Join(errs...)
}

func (f *Feed) Connected() bool {
	b := f.broker.Load()
	return b != nil && (*b).Connected()
}

func closedWithin(ctx context.Context, cc jetstream.ConsumeContext) bool {
	select {
	case <-cc.Closed():
		return true
	case <-ctx.Done():
		return false
	}
}
