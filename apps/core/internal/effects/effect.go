package effects

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultFetchBatch = 256
	DefaultFetchWait  = time.Second
	DefaultRetryDelay = 5 * time.Second
	DefaultDrain      = time.Second
	DefaultPoll       = time.Second
)

type Effect struct {
	Name  string
	Delay time.Duration
	Run   func(ctx context.Context, recs []work.Record) []error
}

type Registry map[store.ChangeKind][]Effect

type Owner interface {
	Owns(slot uint16) bool
}

type Deps struct {
	Queue    func(partition int) work.Queue
	Owner    Owner
	Registry Registry
}

type Config struct {
	Partitions int
	FetchBatch int
	FetchWait  time.Duration
	RetryDelay time.Duration
	Drain      time.Duration
	Poll       time.Duration
}

type Stats struct {
	Processed uint64
	Failed    uint64
	Lag       time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Partitions = cmp.Or(c.Partitions, work.DefaultPartitions)
	c.FetchBatch = cmp.Or(c.FetchBatch, DefaultFetchBatch)
	c.FetchWait = cmp.Or(c.FetchWait, DefaultFetchWait)
	c.RetryDelay = cmp.Or(c.RetryDelay, DefaultRetryDelay)
	c.Drain = cmp.Or(c.Drain, DefaultDrain)
	c.Poll = cmp.Or(c.Poll, DefaultPoll)
	return c
}

func (c Config) validate() error {
	switch {
	case c.Partitions <= 0 || c.Partitions > slotmap.Count:
		return fmt.Errorf("%w: effect workers need 1 to %d partitions, got %d", apperr.ErrInvalidArgument, slotmap.Count, c.Partitions)
	case c.FetchBatch <= 0 || c.FetchBatch > work.MaxAckPending:
		return fmt.Errorf("%w: fetch batch %d must be between 1 and the consumer max ack pending %d", apperr.ErrInvalidArgument, c.FetchBatch, work.MaxAckPending)
	case c.FetchWait <= 0 || c.RetryDelay <= 0 || c.Drain <= 0 || c.Poll <= 0:
		return fmt.Errorf("%w: effect worker config %+v must be positive", apperr.ErrInvalidArgument, c)
	default:
		return nil
	}
}

func (r Registry) validate() error {
	for kind, effs := range r {
		for _, e := range effs {
			if e.Name == "" || e.Run == nil || e.Delay < 0 {
				return fmt.Errorf("%w: effect %q for change kind %d needs a name, a run function and a delay of at least 0", apperr.ErrInvalidArgument, e.Name, kind)
			}
		}
	}
	return nil
}
