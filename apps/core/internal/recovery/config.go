package recovery

import (
	"cmp"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultInterval     = 30 * time.Second
	DefaultRemoveAfter  = 15 * time.Second
	DefaultRedisTimeout = time.Second
	DefaultBatch        = 10000
	DefaultWorkers      = 8

	removeSkew   = time.Second
	removeChunk  = 1000
	maxBatchSize = 100000
)

type Config struct {
	Interval      time.Duration
	RemoveAfter   time.Duration
	GroupDeadline time.Duration
	RoomTimeout   time.Duration
	RedisTimeout  time.Duration
	Batch         int
	Workers       int
}

func (c Config) withDefaults() Config {
	c.Interval = cmp.Or(c.Interval, DefaultInterval)
	c.RemoveAfter = cmp.Or(c.RemoveAfter, DefaultRemoveAfter)
	c.RoomTimeout = cmp.Or(c.RoomTimeout, 2*c.GroupDeadline)
	c.RedisTimeout = cmp.Or(c.RedisTimeout, DefaultRedisTimeout)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.Workers = cmp.Or(c.Workers, DefaultWorkers)
	return c
}

func (c Config) minRemoveAfter() time.Duration {
	return actor.ActiveMarkEvery + c.GroupDeadline + removeSkew
}

func (c Config) validate() error {
	switch {
	case c.GroupDeadline <= 0:
		return fmt.Errorf("%w: recovery needs the actor group deadline, got %v", apperr.ErrInvalidArgument, c.GroupDeadline)
	case c.Interval <= 0 || c.RoomTimeout <= 0 || c.RedisTimeout <= 0 || c.Batch <= 0 || c.Workers <= 0:
		return fmt.Errorf("%w: recovery config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.RemoveAfter < c.minRemoveAfter():
		return fmt.Errorf("%w: recovery remove-after %v must cover the %v mark interval, the %v group deadline and %v of clock skew", apperr.ErrInvalidArgument, c.RemoveAfter, actor.ActiveMarkEvery, c.GroupDeadline, removeSkew)
	case c.RoomTimeout <= c.GroupDeadline:
		return fmt.Errorf("%w: recovery room timeout %v must exceed the group deadline %v", apperr.ErrInvalidArgument, c.RoomTimeout, c.GroupDeadline)
	case c.Batch > maxBatchSize:
		return fmt.Errorf("%w: recovery batch %d exceeds %d rooms", apperr.ErrInvalidArgument, c.Batch, maxBatchSize)
	default:
		return nil
	}
}
