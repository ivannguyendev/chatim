package notify

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/modules/mention/internal/config"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

func EnsureStream(ctx context.Context, admin wspublish.Admin, cfg config.Config) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	return wspublish.EnsureStream(ctx, admin, StreamConfig(cfg))
}

func StreamConfig(cfg config.Config) jetstream.StreamConfig {
	cfg = cfg.WithDefaults()
	return wspublish.StreamConfig(cfg.WsPublishStream, cfg.WsPublishRoot, cfg.Replicas, wspublish.DefaultMaxAge, wspublish.DefaultDuplicates)
}
