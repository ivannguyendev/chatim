package redisguard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Config struct {
	Name      string
	Timeout   time.Duration
	Cooldown  time.Duration
	Skipped   error
	Degraded  string
	Recovered string
	Now       func() time.Time
}

type transition int

const (
	steady transition = iota
	entered
	recovered
)

type Guard struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	degraded bool
	epoch    uint64
	retryAt  time.Time
}

func CheckClient(rdb *redis.Client, user string) error {
	if rdb == nil {
		return fmt.Errorf("%w: %s needs a redis client", apperr.ErrInvalidArgument, user)
	}
	if !rdb.Options().ContextTimeoutEnabled {
		return fmt.Errorf("%w: %s redis client must enable ContextTimeoutEnabled so call timeouts bound socket waits", apperr.ErrInvalidArgument, user)
	}
	return nil
}

func New(cfg Config, log *slog.Logger) (*Guard, error) {
	switch {
	case cfg.Name == "" || cfg.Degraded == "" || cfg.Recovered == "" || cfg.Skipped == nil:
		return nil, fmt.Errorf("%w: redis guard needs a name, log messages and a skip error", apperr.ErrInvalidArgument)
	case cfg.Timeout <= 0 || cfg.Cooldown <= 0:
		return nil, fmt.Errorf("%w: %s timeout %v and cooldown %v must be positive", apperr.ErrInvalidArgument, cfg.Name, cfg.Timeout, cfg.Cooldown)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Guard{cfg: cfg, log: log}, nil
}

func (g *Guard) Do(ctx context.Context, op string, fn func(context.Context) error) error {
	epoch, ok := g.admit(g.cfg.Now())
	if !ok {
		return g.cfg.Skipped
	}
	cctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	err := fn(cctx)
	if err != nil {
		err = fmt.Errorf("%s %s: %w", g.cfg.Name, op, err)
	}
	if err == nil || ctx.Err() == nil {
		g.observe(ctx, epoch, op, err, g.cfg.Now())
	}
	return err
}

func (g *Guard) admit(now time.Time) (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.degraded {
		if now.Before(g.retryAt) {
			return 0, false
		}
		g.retryAt = now.Add(g.cfg.Cooldown)
	}
	return g.epoch, true
}

func (g *Guard) observe(ctx context.Context, epoch uint64, op string, err error, now time.Time) {
	switch g.record(epoch, err != nil, now) {
	case entered:
		g.log.WarnContext(ctx, g.cfg.Degraded, "op", op, "err", err, "cooldown", g.cfg.Cooldown)
	case recovered:
		g.log.InfoContext(ctx, g.cfg.Recovered, "op", op)
	default:
	}
}

func (g *Guard) record(epoch uint64, failed bool, now time.Time) transition {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case failed && !g.degraded:
		g.degraded, g.epoch, g.retryAt = true, g.epoch+1, now.Add(g.cfg.Cooldown)
		return entered
	case failed:
		g.retryAt = now.Add(g.cfg.Cooldown)
		return steady
	case g.degraded && epoch == g.epoch:
		g.degraded, g.epoch = false, g.epoch+1
		return recovered
	default:
		return steady
	}
}
