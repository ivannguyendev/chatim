package feed

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type Position struct {
	Last    uint64
	Created time.Time
}

type Consumer interface {
	Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error)
}

type Broker interface {
	EnsureStream(ctx context.Context, cfg jetstream.StreamConfig) error
	Position(ctx context.Context, stream string) (Position, error)
	OrderedConsumer(ctx context.Context, stream string, cfg jetstream.OrderedConsumerConfig) (Consumer, error)
	Connected() bool
	Reconnects() uint64
	Close()
}
