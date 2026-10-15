package clear

import (
	"cmp"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultFlush = 100 * time.Millisecond
	DefaultBatch = 500
	DefaultQueue = 65536
	ClearTimeout = time.Second
	DrainTimeout = time.Second
)

type Config struct {
	Flush time.Duration
	Batch int
	Queue int
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Flush = cmp.Or(c.Flush, DefaultFlush)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.Queue = cmp.Or(c.Queue, DefaultQueue)
	return c
}

func (c Config) validate() error {
	switch {
	case c.Flush <= 0 || c.Batch <= 0 || c.Queue <= 0:
		return fmt.Errorf("%w: state clear config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Batch > c.Queue:
		return fmt.Errorf("%w: state clear batch %d exceeds queue %d", apperr.ErrInvalidArgument, c.Batch, c.Queue)
	default:
		return nil
	}
}
