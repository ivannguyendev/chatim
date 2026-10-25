package wspublish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultMaxAge     = 24 * time.Hour
	DefaultDuplicates = 6 * time.Minute
)

type Admin interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

func StreamConfig(name, root string, replicas int, maxAge, duplicates time.Duration) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:       name,
		Subjects:   []string{Subjects(root)},
		Retention:  jetstream.LimitsPolicy,
		Storage:    jetstream.FileStorage,
		Replicas:   replicas,
		MaxAge:     maxAge,
		Duplicates: duplicates,
	}
}

func EnsureStream(ctx context.Context, admin Admin, cfg jetstream.StreamConfig) error {
	if admin == nil {
		return fmt.Errorf("%w: ensure ws publish stream needs a jetstream client", apperr.ErrInvalidArgument)
	}
	if err := validStreamConfig(cfg); err != nil {
		return err
	}
	if _, err := admin.CreateOrUpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("ensure ws publish stream %s: %w", cfg.Name, err)
	}
	return nil
}

func validStreamConfig(cfg jetstream.StreamConfig) error {
	if err := ValidStream(cfg.Name); err != nil {
		return err
	}
	if len(cfg.Subjects) != 1 || !strings.HasSuffix(cfg.Subjects[0], ".>") {
		return invalid("stream %s needs one subject {root}.>, got %v", cfg.Name, cfg.Subjects)
	}
	if err := ValidRoot(strings.TrimSuffix(cfg.Subjects[0], ".>")); err != nil {
		return err
	}
	switch {
	case cfg.Replicas < 1:
		return invalid("stream %s replicas %d must be positive", cfg.Name, cfg.Replicas)
	case cfg.Duplicates <= 0 || cfg.MaxAge < cfg.Duplicates:
		return invalid("stream %s duplicate window %v must be positive and within the max age %v", cfg.Name, cfg.Duplicates, cfg.MaxAge)
	default:
		return nil
	}
}
