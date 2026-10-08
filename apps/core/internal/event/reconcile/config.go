package reconcile

import (
	"cmp"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	LeaderSlot uint16 = 0

	DefaultWindow       = 1024
	DefaultBatch        = 256
	DefaultConfirmEvery = time.Second
	DefaultDrain        = time.Second
	DefaultPoll         = time.Second
)

type Config struct {
	SubjectRoot  string
	Partitions   int
	Window       int
	Batch        int
	ConfirmEvery time.Duration
	Drain        time.Duration
	Poll         time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Partitions = cmp.Or(c.Partitions, work.DefaultPartitions)
	c.Window = cmp.Or(c.Window, DefaultWindow)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.ConfirmEvery = cmp.Or(c.ConfirmEvery, DefaultConfirmEvery)
	c.Drain = cmp.Or(c.Drain, DefaultDrain)
	c.Poll = cmp.Or(c.Poll, DefaultPoll)
	return c
}

func (c Config) validate() error {
	switch {
	case c.SubjectRoot == "":
		return fmt.Errorf("%w: reconcile needs the work subject root", apperr.ErrInvalidArgument)
	case c.Partitions <= 0 || c.Partitions > slotmap.Count:
		return fmt.Errorf("%w: reconcile partitions %d must be between 1 and the slot count %d", apperr.ErrInvalidArgument, c.Partitions, slotmap.Count)
	case c.ConfirmEvery <= 0 || c.Drain <= 0 || c.Poll <= 0 || c.Window <= 0 || c.Batch <= 0:
		return fmt.Errorf("%w: reconcile config %+v must be positive", apperr.ErrInvalidArgument, c)
	default:
		return nil
	}
}

func (c Config) JetStreamOptions(ackTimeout time.Duration) []jetstream.JetStreamOpt {
	c = c.withDefaults()
	return []jetstream.JetStreamOpt{
		jetstream.WithPublishAsyncMaxPending(c.Window),
		jetstream.WithPublishAsyncTimeout(ackTimeout),
	}
}
