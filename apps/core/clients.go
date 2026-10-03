package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

const slotRedisPool = 4

type clients struct {
	mongo  *mongo.Client
	slots  *redis.Client
	dedupe *redis.Client
	nats   *nats.Conn
	js     jetstream.JetStream
}

func connect(ctx context.Context, cfg config.Config, log *slog.Logger) (*clients, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	c := &clients{}
	err := c.connectMongo(ctx, cfg)
	if err == nil {
		err = c.connectRedis(ctx, cfg)
	}
	if err == nil {
		err = c.connectNATS(cfg, log)
	}
	if err != nil {
		c.close(ctx, log)
		return nil, err
	}
	return c, nil
}

func (c *clients) connectMongo(ctx context.Context, cfg config.Config) error {
	client, err := mongo.Connect(mongoOptions(cfg))
	if err != nil {
		return fmt.Errorf("mongo connect %s: %w", config.RedactURL(cfg.MongoURI), config.RedactError(err, cfg.MongoURI))
	}
	c.mongo = client
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("mongo ping %s: %w", config.RedactURL(cfg.MongoURI), config.RedactError(err, cfg.MongoURI))
	}
	return nil
}

func mongoOptions(cfg config.Config) *options.ClientOptions {
	opts := options.Client().ApplyURI(cfg.MongoURI).
		SetAppName("chatim-core").
		SetConnectTimeout(cfg.ConnectTimeout).
		SetServerSelectionTimeout(cfg.ConnectTimeout)
	if cfg.MongoUser != "" {
		opts.SetAuth(options.Credential{Username: cfg.MongoUser, Password: cfg.MongoPassword, AuthSource: cfg.MongoAuthSource})
	}
	return opts
}

func (c *clients) connectRedis(ctx context.Context, cfg config.Config) error {
	c.slots = redis.NewClient(redisOptions(cfg.RedisAddr, cfg.RedisDB, cfg.RedisPassword, "chatim-core-slots-"+cfg.CoreID, slotRedisPool))
	c.dedupe = redis.NewClient(redisOptions(cfg.RedisDedupeAddr, cfg.RedisDedupeDB, cfg.RedisDedupePassword, "chatim-core-dedupe-"+cfg.CoreID, 0))
	pings := []struct {
		instance string
		rdb      *redis.Client
	}{{"state", c.slots}, {"dedupe", c.dedupe}}
	for _, p := range pings {
		if err := p.rdb.Ping(ctx).Err(); err != nil {
			opts := p.rdb.Options()
			return fmt.Errorf("redis %s ping %s/%d: %w", p.instance, opts.Addr, opts.DB, err)
		}
	}
	return nil
}

func redisOptions(addr string, db int, password, name string, pool int) *redis.Options {
	return &redis.Options{
		Addr:                  addr,
		DB:                    db,
		Password:              password,
		ClientName:            name,
		PoolSize:              pool,
		ContextTimeoutEnabled: true,
	}
}

func (c *clients) connectNATS(cfg config.Config, log *slog.Logger) error {
	nc, err := nats.Connect(cfg.NATSURL, natsOptions(cfg, log)...)
	if err != nil {
		return fmt.Errorf("nats connect %s: %w", config.RedactURL(cfg.NATSURL), config.RedactError(err, cfg.NATSURL))
	}
	c.nats = nc
	js, err := jetstream.New(nc, cfg.Publish.JetStreamOptions(log)...)
	if err != nil {
		return fmt.Errorf("jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.js = js
	return nil
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

func (c *clients) close(ctx context.Context, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.CloseTimeout)
	defer cancel()
	var errs []error
	if c.nats != nil {
		c.nats.Close()
	}
	for _, rdb := range []*redis.Client{c.slots, c.dedupe} {
		if rdb != nil {
			errs = append(errs, rdb.Close())
		}
	}
	if c.mongo != nil {
		errs = append(errs, c.mongo.Disconnect(ctx))
	}
	if err := errors.Join(errs...); err != nil {
		log.WarnContext(ctx, "close clients", "err", err)
	}
}
