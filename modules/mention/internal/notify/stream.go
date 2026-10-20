package notify

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/modules/mention/internal/config"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Admin interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

func Subjects(root string) string { return root + ".>" }

func EnsureStream(ctx context.Context, admin Admin, cfg config.Config) error {
	if admin == nil {
		return fmt.Errorf("%w: ensure notify stream needs a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if _, err := admin.CreateOrUpdateStream(ctx, StreamConfig(cfg)); err != nil {
		return fmt.Errorf("ensure notify stream %s: %w", cfg.NotifyStream, err)
	}
	return nil
}

func StreamConfig(cfg config.Config) jetstream.StreamConfig {
	cfg = cfg.WithDefaults()
	return jetstream.StreamConfig{
		Name:       cfg.NotifyStream,
		Subjects:   []string{Subjects(cfg.NotifySubjectRoot)},
		Retention:  jetstream.LimitsPolicy,
		Storage:    jetstream.FileStorage,
		Replicas:   cfg.Replicas,
		MaxAge:     cfg.NotifyMaxStreamAge,
		Duplicates: cfg.NotifyDuplicates,
	}
}
