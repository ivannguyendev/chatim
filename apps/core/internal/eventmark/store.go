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
	rdb   *redis.Client
	cfg   Config
	guard *redisguard.Guard
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
	guard, err := redisguard.New(redisguard.Config{
		Name:      "event ack marks",
		Timeout:   cfg.Timeout,
		Cooldown:  cfg.Cooldown,
		Skipped:   ErrDegraded,
		Degraded:  "event ack marks degraded; reconciliation republishes unmarked events",
		Recovered: "event ack marks recovered",
		Now:       time.Now,
	}, log)
	if err != nil {
		return nil, err
	}
	return &Store{rdb: rdb, cfg: cfg, guard: guard}, nil
}

func (s *Store) Mark(ctx context.Context, keys []store.MsgKey) error {
	if len(keys) == 0 {
		return nil
	}
	return s.guard.Do(ctx, "mark", func(cctx context.Context) error {
		_, err := s.rdb.Pipelined(cctx, func(p redis.Pipeliner) error {
			touched := make(map[string]bool, len(keys))
			for _, k := range keys {
				chunk := chunkKey(k)
				p.SetBit(cctx, chunk, offset(k), 1)
				if !touched[chunk] {
					touched[chunk] = true
					p.PExpire(cctx, chunk, s.cfg.TTL)
				}
			}
			return nil
		})
		return err
	})
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
