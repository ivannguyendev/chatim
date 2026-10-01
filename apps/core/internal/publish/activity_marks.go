package publish

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var errMarksSkipped = errors.New("active room marks cooling down after a redis failure")

type MarkConfig struct {
	Timeout  time.Duration
	Cooldown time.Duration
}

type ActivityMarks struct {
	rdb   *redis.Client
	guard *redisguard.Guard
}

func NewActivityMarks(rdb *redis.Client, cfg MarkConfig, log *slog.Logger) (*ActivityMarks, error) {
	if err := redisguard.CheckClient(rdb, "activity marks"); err != nil {
		return nil, err
	}
	guard, err := redisguard.New(redisguard.Config{
		Name:      "active room mark",
		Timeout:   cmp.Or(cfg.Timeout, DefaultRedisTimeout),
		Cooldown:  cmp.Or(cfg.Cooldown, DefaultRedisCooldown),
		Skipped:   errMarksSkipped,
		Degraded:  "active room marks degraded; sends continue without recovery marks",
		Recovered: "active room marks recovered",
	}, log)
	if err != nil {
		return nil, err
	}
	return &ActivityMarks{rdb: rdb, guard: guard}, nil
}

func (m *ActivityMarks) MarkActive(ctx context.Context, room uint64) error {
	return m.guard.Do(ctx, "mark", func(cctx context.Context) error {
		z := redis.Z{Score: float64(time.Now().UnixMilli()), Member: pbconv.RoomID(room)}
		return m.rdb.ZAdd(cctx, ActiveKey(slotmap.Of(room)), z).Err()
	})
}
