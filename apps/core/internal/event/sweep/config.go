package sweep

import (
	"cmp"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	LeaderSlot uint16 = 0

	DefaultInterval   = 2 * time.Second
	DefaultMinAge     = 15 * time.Second
	DefaultLookback   = 60 * time.Second
	DefaultBatch      = 500
	DefaultRate       = 5000
	DefaultTick       = time.Second
	DefaultAckTimeout = publish.DefaultAckTimeout
)

type Config struct {
	SubjectRoot string
	Interval    time.Duration
	MinAge      time.Duration
	Lookback    time.Duration
	Batch       int
	Rate        int
	Tick        time.Duration
	AckTimeout  time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Interval = cmp.Or(c.Interval, DefaultInterval)
	c.MinAge = cmp.Or(c.MinAge, DefaultMinAge)
	c.Lookback = cmp.Or(c.Lookback, DefaultLookback)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.Rate = cmp.Or(c.Rate, DefaultRate)
	c.Tick = cmp.Or(c.Tick, DefaultTick)
	c.AckTimeout = cmp.Or(c.AckTimeout, DefaultAckTimeout)
	return c
}

func (c Config) validate() error {
	switch {
	case c.SubjectRoot == "":
		return fmt.Errorf("%w: sweeper needs the event subject root", apperr.ErrInvalidArgument)
	case c.Interval <= 0 || c.MinAge <= 0 || c.Lookback <= 0 || c.Tick <= 0 || c.AckTimeout <= 0 || c.Rate <= 0:
		return fmt.Errorf("%w: sweeper config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Batch <= 0 || c.Batch > store.MaxPendingPage:
		return fmt.Errorf("%w: sweeper batch %d must be between 1 and %d", apperr.ErrInvalidArgument, c.Batch, store.MaxPendingPage)
	default:
		return nil
	}
}
