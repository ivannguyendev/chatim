package publish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var errMarksSkipped = errors.New("active room marks cooling down after a redis failure")

var markScript = redis.NewScript(`
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
if not redis.call('SET', KEYS[2], ARGV[3], 'NX', 'PX', ARGV[4]) then
  redis.call('PEXPIRE', KEYS[2], ARGV[4])
end
return 1`)

type MarkConfig struct {
	Timeout      time.Duration
	Cooldown     time.Duration
	WatermarkTTL time.Duration
}

type ActivityMarks struct {
	rdb   *redis.Client
	guard *redisguard.Guard
	ttl   time.Duration
}

func NewActivityMarks(rdb *redis.Client, cfg MarkConfig, log *slog.Logger) (*ActivityMarks, error) {
	if err := redisguard.CheckClient(rdb, "activity marks"); err != nil {
		return nil, err
	}
	ttl := cmp.Or(cfg.WatermarkTTL, DefaultWatermarkTTL)
	if ttl < time.Millisecond {
		return nil, fmt.Errorf("%w: activity mark watermark ttl %v must be at least 1ms", apperr.ErrInvalidArgument, ttl)
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
	return &ActivityMarks{rdb: rdb, guard: guard, ttl: ttl}, nil
}

func (m *ActivityMarks) Mark(ctx context.Context, room, last uint64) error {
	return m.guard.Do(ctx, "mark", func(cctx context.Context) error {
		keys := []string{ActiveKey(slotmap.Of(room)), WatermarkKey(room)}
		args := []any{time.Now().UnixMilli(), pbconv.RoomID(room), strconv.FormatUint(last, 10), m.ttl.Milliseconds()}
		return markScript.Run(cctx, m.rdb, keys, args...).Err()
	})
}
