package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

const (
	DurableName     = "media"
	FetchWait       = 250 * time.Millisecond
	StuckDeliveries = uint64(6)
)

type Config struct {
	Stream      string
	SubjectRoot string
	FetchBatch  int
	Workers     int
	AckWait     time.Duration
	SyncTimeout time.Duration
}

func (c Config) Validate() error {
	if c.Stream == "" || c.SubjectRoot == "" || c.FetchBatch < 1 || c.Workers < 1 || c.AckWait < 3*time.Second || c.SyncTimeout <= 0 {
		return fmt.Errorf("%w: media consumer needs a stream, a subject root, a batch and workers of at least 1, an ack wait of at least 3s and a sync timeout", apperr.ErrInvalidArgument)
	}
	return nil
}

func StopTimeout() time.Duration { return FetchWait + jsconsume.FetchExpiryGrace }

type Admin interface {
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func EnsureConsumer(ctx context.Context, admin Admin, cfg Config) error {
	if admin == nil {
		return fmt.Errorf("%w: ensure media consumer needs a jetstream client", apperr.ErrInvalidArgument)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if _, err := admin.CreateOrUpdateConsumer(ctx, cfg.Stream, ConsumerConfig(cfg)); err != nil {
		return fmt.Errorf("ensure media consumer %s on %s: %w", DurableName, cfg.Stream, err)
	}
	return nil
}

func NewSource(js jsconsume.ConsumerLookup, cfg Config) *jsconsume.PullSource {
	return jsconsume.NewPullSource(js, cfg.Stream, DurableName)
}

func ConsumerConfig(cfg Config) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:        DurableName,
		FilterSubjects: Subjects(cfg.SubjectRoot),
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        cfg.AckWait,
		DeliverPolicy:  jetstream.DeliverNewPolicy,
	}
}

func Subjects(root string) []string {
	return []string{
		root + ".*.message.*.msg_created",
		root + ".*.mention.*.msg_created",
		root + ".*.message.*.msg_deleted",
	}
}
