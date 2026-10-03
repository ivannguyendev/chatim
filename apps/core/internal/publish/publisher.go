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
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
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
}

type Publisher struct {
	shards  []*shard
	log     *slog.Logger
	mu      sync.RWMutex
	closed  bool
	started atomic.Bool
	abort   chan struct{}
	stop    sync.Once
	done    chan struct{}
}

func New(js JetStream, rdb *redis.Client, cfg Config, log *slog.Logger) (*Publisher, error) {
	if js == nil {
		return nil, fmt.Errorf("%w: publisher needs a jetstream client", apperr.ErrInvalidArgument)
	}
	if err := redisguard.CheckClient(rdb, "publisher"); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	guard, err := redisguard.New(redisguard.Config{
		Name:      "publish watermark",
		Timeout:   cfg.RedisTimeout,
		Cooldown:  cfg.RedisCooldown,
		Skipped:   errWatermarksSkipped,
		Degraded:  "publish watermarks degraded; they stall until redis returns",
		Recovered: "publish watermarks recovered",
	}, log)
	if err != nil {
		return nil, err
	}
	store := &watermarkStore{rdb: rdb, guard: guard, ttl: cfg.WatermarkTTL}
	p := &Publisher{shards: make([]*shard, cfg.Shards), log: log, abort: make(chan struct{}), done: make(chan struct{})}
	for i := range p.shards {
		p.shards[i] = &shard{
			js:    js,
			store: store,
			cfg:   cfg,
			log:   log,
			queue: make(chan item, cfg.QueueSize),
			abort: p.abort,
			rooms: newTracker(cfg),
		}
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
		wg.Go(func() { s.run(ctx) })
	}
	wg.Wait()
	return ctx.Err()
}

func (p *Publisher) Enqueue(room uint64, events []*chatimv1.Event) error {
	if len(events) == 0 {
		return nil
	}
	return p.offer(item{room: room, events: events})
}

func (p *Publisher) Skip(room uint64, pts []uint64) error {
	if len(pts) == 0 {
		return nil
	}
	return p.offer(item{room: room, skips: pts})
}

func (p *Publisher) offer(it item) error {
	s := p.shards[int(slotmap.Of(it.room))%len(p.shards)]
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	select {
	case s.queue <- it:
		s.full.Store(false)
		return nil
	default:
		if s.full.CompareAndSwap(false, true) {
			p.log.Warn("publish queue full; dropping events until recovery republishes them", "room", it.room, "events", len(it.events), "skips", len(it.skips))
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
		return nil
	case <-ctx.Done():
		p.stop.Do(func() { close(p.abort) })
		<-p.done
		return ctx.Err()
	}
}
