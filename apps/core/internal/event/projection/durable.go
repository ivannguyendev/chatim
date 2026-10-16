package projection

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const DurableName = "projections"

var LegacyDurables = [...]string{"projection-room_activity", "projection-mention_reply_index", "projection-mention_read"}

type Admin interface {
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
	DeleteConsumer(ctx context.Context, stream, consumer string) error
}

func EnsureConsumer(ctx context.Context, js Admin, cfg Config) error {
	if js == nil {
		return fmt.Errorf("%w: ensure projection consumer needs a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}
	if _, err := js.CreateOrUpdateConsumer(ctx, cfg.Stream, consumerConfig(cfg)); err != nil {
		return fmt.Errorf("ensure projection consumer %s on %s: %w", DurableName, cfg.Stream, err)
	}
	for _, name := range LegacyDurables {
		if err := js.DeleteConsumer(ctx, cfg.Stream, name); err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
			return fmt.Errorf("delete legacy projection consumer %s on %s: %w", name, cfg.Stream, err)
		}
	}
	return nil
}

func NewSource(js eventkit.ConsumerLookup, cfg Config) *eventkit.PullSource {
	return eventkit.NewPullSource(js, cfg.Stream, DurableName)
}

func consumerConfig(cfg Config) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       DurableName,
		FilterSubject: cfg.SubjectRoot + ".>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       AckWait,
		MaxAckPending: cfg.MaxAckPending(),
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}
