package eventmark

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultTTL      = time.Hour
	DefaultTimeout  = 100 * time.Millisecond
	DefaultCooldown = time.Second

	keyPrefix = "chatim:evtack:"
	chunkBits = 13
	chunkMask = 1<<chunkBits - 1
)

var ErrDegraded = fmt.Errorf("event ack marks cooling down after a redis failure: %w", apperr.ErrUnavailable)

type Config struct {
	TTL      time.Duration
	Timeout  time.Duration
	Cooldown time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.TTL = cmp.Or(c.TTL, DefaultTTL)
	c.Timeout = cmp.Or(c.Timeout, DefaultTimeout)
	c.Cooldown = cmp.Or(c.Cooldown, DefaultCooldown)
	return c
}

func (c Config) validate() error {
	if c.TTL < time.Second || c.Timeout <= 0 || c.Cooldown <= 0 {
		return fmt.Errorf("%w: event ack marks ttl %v must be at least 1s, timeout %v and cooldown %v positive", apperr.ErrInvalidArgument, c.TTL, c.Timeout, c.Cooldown)
	}
	return nil
}

type Store struct {
	rdb     *redis.Client
	cfg     Config
	guard   *redisguard.Guard
	now     func() time.Time
	refresh *refreshLog
}

func New(rdb *redis.Client, cfg Config, log *slog.Logger) (*Store, error) {
	if err := redisguard.CheckClient(rdb, "event ack marks"); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Store{rdb: rdb, cfg: cfg, now: time.Now, refresh: newRefreshLog(cfg.TTL)}
	guard, err := redisguard.New(redisguard.Config{
		Name:      "event ack marks",
		Timeout:   cfg.Timeout,
		Cooldown:  cfg.Cooldown,
		Skipped:   ErrDegraded,
		Degraded:  "event ack marks degraded; reconciliation republishes unmarked events",
		Recovered: "event ack marks recovered",
		Now:       func() time.Time { return s.now() },
	}, log)
	if err != nil {
		return nil, err
	}
	s.guard = guard
	return s, nil
}

func (s *Store) Mark(ctx context.Context, keys []store.MsgKey) error {
	if len(keys) == 0 {
		return nil
	}
	chunks := make([]string, len(keys))
	for i, k := range keys {
		chunks[i] = chunkKey(k)
	}
	now := s.now()
	stale := s.refresh.stale(chunks, now)
	err := s.guard.Do(ctx, "mark", func(cctx context.Context) error {
		_, err := s.rdb.Pipelined(cctx, func(p redis.Pipeliner) error {
			for i, k := range keys {
				p.SetBit(cctx, chunks[i], offset(k), 1)
			}
			for _, chunk := range stale {
				p.PExpire(cctx, chunk, s.cfg.TTL)
			}
			return nil
		})
		return err
	})
	if err == nil {
		s.refresh.record(stale, now)
	}
	return err
}

func (s *Store) Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	out := make([]bool, len(keys))
	err := s.guard.Do(ctx, "acked", func(cctx context.Context) error {
		bits := make([]*redis.IntCmd, len(keys))
		_, err := s.rdb.Pipelined(cctx, func(p redis.Pipeliner) error {
			for i, k := range keys {
				bits[i] = p.GetBit(cctx, chunkKey(k), offset(k))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for i, b := range bits {
			out[i] = b.Val() == 1
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func chunkKey(k store.MsgKey) string {
	return keyPrefix + strconv.FormatUint(k.Room, 10) + ":" + strconv.FormatUint(k.Thread, 10) + ":" + strconv.FormatUint(k.Seq>>chunkBits, 10)
}

func offset(k store.MsgKey) int64 {
	bit := k.Seq & chunkMask
	if bit > math.MaxInt64 {
		return 0
	}
	return int64(bit)
}

func (s *Store) Degraded() bool { return s.guard.Degraded() }
