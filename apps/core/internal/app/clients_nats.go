package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

func (c *clients) connectNATS(cfg config.Config, log *slog.Logger) error {
	nc, err := nats.Connect(cfg.NATSURL, natsOptions(cfg, log)...)
	if err != nil {
		return fmt.Errorf("nats connect %s: %w", config.RedactURL(cfg.NATSURL), config.RedactError(err, cfg.NATSURL))
	}
	c.nats = nc
	c.pubCounters = &publish.Counters{}
	js, err := jetstream.New(nc, cfg.Publish.JetStreamOptions(log, c.pubCounters)...)
	if err != nil {
		return fmt.Errorf("jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.js = js
	sjs, err := jetstream.New(nc,
		jetstream.WithPublishAsyncMaxPending(sweepPendingPages*cfg.Sweep.Batch),
		jetstream.WithPublishAsyncTimeout(cfg.Publish.AckTimeout),
	)
	if err != nil {
		return fmt.Errorf("sweeper jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.sweepJS = sjs
	njs, err := jetstream.New(nc, notifyOptions(cfg, log)...)
	if err != nil {
		return fmt.Errorf("notify jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.notifyJS = njs
	return nil
}

func notifyOptions(cfg config.Config, log *slog.Logger) []jetstream.JetStreamOpt {
	failures := jsconsume.NewLimitedLog(log)
	return []jetstream.JetStreamOpt{
		jetstream.WithPublishAsyncMaxPending(notifyMaxPending),
		jetstream.WithPublishAsyncTimeout(cfg.Publish.AckTimeout),
		jetstream.WithPublishAsyncErrHandler(func(_ jetstream.JetStream, m *nats.Msg, err error) {
			failures.Warn(context.Background(), "mention notification publish failed; the event comes back", "id", m.Header.Get(jetstream.MsgIDHeader), "err", config.RedactError(err, cfg.NATSURL))
		}),
	}
}

func natsOptions(cfg config.Config, log *slog.Logger) []nats.Option {
	return []nats.Option{
		nats.Name("chatim-core-" + cfg.CoreID),
		nats.Timeout(cfg.ConnectTimeout),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats disconnected", "err", config.RedactError(err, cfg.NATSURL))
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) { log.Info("nats reconnected") }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Warn("nats async error", "err", config.RedactError(err, cfg.NATSURL))
		}),
	}
}
