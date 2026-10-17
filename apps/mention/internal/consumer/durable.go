package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/mention/internal/config"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

const (
	DurableName = "mention"
	AckWait     = 30 * time.Second
)

type Admin interface {
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func EnsureConsumer(ctx context.Context, admin Admin, cfg config.Config) error {
	if admin == nil {
		return fmt.Errorf("%w: ensure mention consumer needs a jetstream client", apperr.ErrInvalidArgument)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg = cfg.WithDefaults()
	if _, err := admin.CreateOrUpdateConsumer(ctx, cfg.EventStream, ConsumerConfig(cfg)); err != nil {
		return fmt.Errorf("ensure mention consumer %s on %s: %w", DurableName, cfg.EventStream, err)
	}
	return nil
}

func NewSource(js jsconsume.ConsumerLookup, cfg config.Config) *jsconsume.PullSource {
	return jsconsume.NewPullSource(js, cfg.EventStream, DurableName)
}

func ConsumerConfig(cfg config.Config) jetstream.ConsumerConfig {
	cfg = cfg.WithDefaults()
	return jetstream.ConsumerConfig{
		Durable:        DurableName,
		FilterSubjects: Subjects(cfg.EventSubjectRoot),
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        AckWait,
		MaxAckPending:  cfg.MaxAckPending,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	}
}

func Subjects(root string) []string {
	return []string{
		root + ".*.mention.*.msg_created",
		root + ".*.message.*.msg_edited",
		root + ".*.message.*.msg_deleted",
	}
}
