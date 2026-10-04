package reconcile

import (
	"cmp"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	LeaderSlot uint16 = 0

	DefaultDelay        = 30 * time.Second
	DefaultWindow       = 1024
	DefaultBatch        = 256
	DefaultConfirmEvery = time.Second
	DefaultDrain        = time.Second
	DefaultPoll         = time.Second
	DefaultRoomCache    = 65536
)

type Config struct {
	SubjectRoot     string
	Delay           time.Duration
	DuplicateWindow time.Duration
	Window          int
	Batch           int
	ConfirmEvery    time.Duration
	Drain           time.Duration
	Poll            time.Duration
	RoomCache       int
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Delay = cmp.Or(c.Delay, DefaultDelay)
	c.Window = cmp.Or(c.Window, DefaultWindow)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.ConfirmEvery = cmp.Or(c.ConfirmEvery, DefaultConfirmEvery)
	c.Drain = cmp.Or(c.Drain, DefaultDrain)
	c.Poll = cmp.Or(c.Poll, DefaultPoll)
	c.RoomCache = cmp.Or(c.RoomCache, DefaultRoomCache)
	return c
}

func (c Config) validate() error {
	switch {
	case c.SubjectRoot == "":
		return fmt.Errorf("%w: reconcile needs the event subject root", apperr.ErrInvalidArgument)
	case c.Delay <= 0 || c.ConfirmEvery <= 0 || c.Drain <= 0 || c.Poll <= 0 || c.Window <= 0 || c.Batch <= 0 || c.RoomCache <= 0:
		return fmt.Errorf("%w: reconcile config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.DuplicateWindow <= c.Delay:
		return fmt.Errorf("%w: reconcile delay %v must be shorter than the stream duplicate window %v", apperr.ErrInvalidArgument, c.Delay, c.DuplicateWindow)
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
