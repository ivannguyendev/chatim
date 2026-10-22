package feed

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/redact"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

const (
	DefaultReadyTimeout = 10 * time.Second
	pingInterval        = 10 * time.Second
	maxPingsOut         = 3
	noReconnectBuf      = -1
)

var errNotConnected = errors.New("nats not connected")

type NATSConfig struct {
	URL          string
	Name         string
	ReadyTimeout time.Duration
}

type natsBroker struct {
	nc *nats.Conn
	js jetstream.JetStream
}

func Dial(ctx context.Context, cfg NATSConfig, log *slog.Logger) (Broker, error) {
	connected := make(chan struct{})
	var once sync.Once
	nc, err := nats.Connect(cfg.URL, natsOptions(cfg, log, func() { once.Do(func() { close(connected) }) })...)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", redact.URL(cfg.URL), redact.Error(err, cfg.URL))
	}
	if err := awaitConnected(ctx, nc, connected, cmp.Or(cfg.ReadyTimeout, DefaultReadyTimeout)); err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats %s: %w", redact.URL(cfg.URL), err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", redact.Error(err, cfg.URL))
	}
	return &natsBroker{nc: nc, js: js}, nil
}

func awaitConnected(ctx context.Context, nc *nats.Conn, connected <-chan struct{}, limit time.Duration) error {
	if nc.IsConnected() {
		return nil
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-connected:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w within %v", errNotConnected, limit)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func natsOptions(cfg NATSConfig, log *slog.Logger, onConnect func()) []nats.Option {
	return []nats.Option{
		nats.Name(cfg.Name),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
		nats.PingInterval(pingInterval),
		nats.MaxPingsOutstanding(maxPingsOut),
		nats.ReconnectBufSize(noReconnectBuf),
		nats.ConnectHandler(func(*nats.Conn) { onConnect() }),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats disconnected", "err", redact.Error(err, cfg.URL))
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			onConnect()
			log.Info("nats reconnected")
		}),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Warn("nats async error", "err", redact.Error(err, cfg.URL))
		}),
	}
}

func (b *natsBroker) EnsureStream(ctx context.Context, cfg jetstream.StreamConfig) error {
	return wspublish.EnsureStream(ctx, b.js, cfg)
}

func (b *natsBroker) Position(ctx context.Context, stream string) (Position, error) {
	s, err := b.js.Stream(ctx, stream)
	if err != nil {
		return Position{}, fmt.Errorf("stream %s: %w", stream, err)
	}
	info := s.CachedInfo()
	return Position{Last: info.State.LastSeq, Created: info.Created}, nil
}

func (b *natsBroker) OrderedConsumer(ctx context.Context, stream string, cfg jetstream.OrderedConsumerConfig) (Consumer, error) {
	c, err := b.js.OrderedConsumer(ctx, stream, cfg)
	if err != nil {
		return nil, fmt.Errorf("ordered consumer on %s: %w", stream, err)
	}
	return c, nil
}

func (b *natsBroker) Connected() bool { return b.nc.IsConnected() }

func (b *natsBroker) Reconnects() uint64 { return b.nc.Stats().Reconnects }

func (b *natsBroker) Close() { b.nc.Close() }
