package feed

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"
)

type Consumer interface {
	Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error)
}

type Broker interface {
	EnsureStream(ctx context.Context, cfg jetstream.StreamConfig) error
	LastSeq(ctx context.Context, stream string) (uint64, error)
	OrderedConsumer(ctx context.Context, stream string, cfg jetstream.OrderedConsumerConfig) (Consumer, error)
	Connected() bool
	Reconnects() uint64
	Close()
}
