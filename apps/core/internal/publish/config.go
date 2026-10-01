package publish

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultShards        = 4
	DefaultQueueSize     = 1024
	DefaultMaxPending    = 256
	DefaultMaxRetrying   = 1024
	DefaultAttempts      = 4
	DefaultRetryBackoff  = 100 * time.Millisecond
	DefaultMaxBackoff    = 2 * time.Second
	DefaultAckTimeout    = 2 * time.Second
	DefaultFlushEvery    = 50 * time.Millisecond
	DefaultWatermarkTTL  = 7 * 24 * time.Hour
	DefaultRedisTimeout  = 100 * time.Millisecond
	DefaultRedisCooldown = time.Second
	DefaultRoomIdle      = 10 * time.Second
	DefaultMaxAhead      = 1024
	DefaultMaxRooms      = 16384

	syncChunk  = 256
	syncPasses = 2
)

type Config struct {
	SubjectRoot   string
	Shards        int
	QueueSize     int
	MaxPending    int
	MaxRetrying   int
	Attempts      int
	RetryBackoff  time.Duration
	MaxBackoff    time.Duration
	AckTimeout    time.Duration
	FlushEvery    time.Duration
	WatermarkTTL  time.Duration
	RedisTimeout  time.Duration
	RedisCooldown time.Duration
	RoomIdle      time.Duration
	MaxAhead      int
	MaxRooms      int
}

func (c Config) withDefaults() Config {
	c.Shards = cmp.Or(c.Shards, DefaultShards)
	c.QueueSize = cmp.Or(c.QueueSize, DefaultQueueSize)
	c.MaxPending = cmp.Or(c.MaxPending, DefaultMaxPending)
	c.MaxRetrying = cmp.Or(c.MaxRetrying, DefaultMaxRetrying)
	c.Attempts = cmp.Or(c.Attempts, DefaultAttempts)
	c.RetryBackoff = cmp.Or(c.RetryBackoff, DefaultRetryBackoff)
	c.MaxBackoff = cmp.Or(c.MaxBackoff, DefaultMaxBackoff)
	c.AckTimeout = cmp.Or(c.AckTimeout, DefaultAckTimeout)
	c.FlushEvery = cmp.Or(c.FlushEvery, DefaultFlushEvery)
	c.WatermarkTTL = cmp.Or(c.WatermarkTTL, DefaultWatermarkTTL)
	c.RedisTimeout = cmp.Or(c.RedisTimeout, DefaultRedisTimeout)
	c.RedisCooldown = cmp.Or(c.RedisCooldown, DefaultRedisCooldown)
	c.RoomIdle = cmp.Or(c.RoomIdle, DefaultRoomIdle)
	c.MaxAhead = cmp.Or(c.MaxAhead, DefaultMaxAhead)
	c.MaxRooms = cmp.Or(c.MaxRooms, DefaultMaxRooms)
	return c
}

func (c Config) validate() error {
	counts := []int{c.Shards, c.QueueSize, c.MaxPending, c.MaxRetrying, c.Attempts, c.MaxAhead, c.MaxRooms}
	spans := []time.Duration{c.RetryBackoff, c.MaxBackoff, c.AckTimeout, c.FlushEvery, c.WatermarkTTL, c.RedisTimeout, c.RedisCooldown, c.RoomIdle}
	switch {
	case !validToken(c.SubjectRoot):
		return fmt.Errorf("%w: publish subject root %q must be one subject token", apperr.ErrInvalidArgument, c.SubjectRoot)
	case !allPositive(counts) || !allPositive(spans):
		return fmt.Errorf("%w: publish config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Shards > slotmap.Count:
		return fmt.Errorf("%w: publish shards %d exceed %d slots", apperr.ErrInvalidArgument, c.Shards, slotmap.Count)
	case c.RetryBackoff > c.MaxBackoff:
		return fmt.Errorf("%w: publish retry backoff %v exceeds its cap %v", apperr.ErrInvalidArgument, c.RetryBackoff, c.MaxBackoff)
	case c.WatermarkTTL < time.Millisecond:
		return fmt.Errorf("%w: publish watermark ttl %v must be at least 1ms", apperr.ErrInvalidArgument, c.WatermarkTTL)
	default:
		return nil
	}
}

func (c Config) JetStreamOptions() []jetstream.JetStreamOpt {
	c = c.withDefaults()
	return []jetstream.JetStreamOpt{
		jetstream.WithPublishAsyncMaxPending(2 * c.Shards * c.MaxPending),
		jetstream.WithPublishAsyncTimeout(c.AckTimeout),
	}
}

func allPositive[T int | time.Duration](vs []T) bool {
	for _, v := range vs {
		if v <= 0 {
			return false
		}
	}
	return true
}

func validToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ".*> \t\r\n")
}
