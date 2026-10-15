package projection

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Admin interface {
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func DurableName(projection string) string { return "projection-" + projection }

func EnsureConsumer(ctx context.Context, js Admin, cfg Config, p Projection) error {
	if js == nil || p == nil {
		return fmt.Errorf("%w: ensure projection consumer needs a jetstream client and a projection", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}
	name := DurableName(p.Name())
	if _, err := js.CreateOrUpdateConsumer(ctx, cfg.Stream, consumerConfig(name, p.Subjects(cfg.SubjectRoot))); err != nil {
		return fmt.Errorf("ensure projection consumer %s on %s: %w", name, cfg.Stream, err)
	}
	return nil
}

func NewSource(js eventkit.ConsumerLookup, cfg Config, p Projection) eventkit.Source {
	return eventkit.NewPullSource(js, cfg.Stream, DurableName(p.Name()))
}

func consumerConfig(name string, subjects []string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:        name,
		FilterSubjects: subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        AckWait,
		MaxAckPending:  MaxAckPending,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	}
}
