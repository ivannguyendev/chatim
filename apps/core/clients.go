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
	mongo *mongo.Client
	redis *redis.Client
	slots *redis.Client
	nats  *nats.Conn
	js    jetstream.JetStream
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
	opts := options.Client().ApplyURI(cfg.MongoURI).
		SetAppName("chatim-core").
		SetConnectTimeout(cfg.ConnectTimeout).
		SetServerSelectionTimeout(cfg.ConnectTimeout)
	client, err := mongo.Connect(opts)
	if err != nil {
		return fmt.Errorf("mongo connect %s: %w", config.RedactURL(cfg.MongoURI), config.RedactError(err, cfg.MongoURI))
	}
	c.mongo = client
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("mongo ping %s: %w", config.RedactURL(cfg.MongoURI), config.RedactError(err, cfg.MongoURI))
	}
	return nil
}

func (c *clients) connectRedis(ctx context.Context, cfg config.Config) error {
	c.redis = redis.NewClient(redisOptions(cfg, "chatim-core-"+cfg.CoreID, 0))
	c.slots = redis.NewClient(redisOptions(cfg, "chatim-core-slots-"+cfg.CoreID, slotRedisPool))
	for _, rdb := range []*redis.Client{c.redis, c.slots} {
		if err := rdb.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis ping %s/%d: %w", cfg.RedisAddr, cfg.RedisDB, err)
		}
	}
	return nil
}

func redisOptions(cfg config.Config, name string, pool int) *redis.Options {
	return &redis.Options{
		Addr:                  cfg.RedisAddr,
		DB:                    cfg.RedisDB,
		ClientName:            name,
		PoolSize:              pool,
		ContextTimeoutEnabled: true,
	}
}

func (c *clients) connectNATS(cfg config.Config, log *slog.Logger) error {
	nc, err := nats.Connect(cfg.NATSURL,
		nats.Name("chatim-core-"+cfg.CoreID),
		nats.Timeout(cfg.ConnectTimeout),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats disconnected", "err", err)
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) { log.Info("nats reconnected") }),
	)
	if err != nil {
		return fmt.Errorf("nats connect %s: %w", config.RedactURL(cfg.NATSURL), config.RedactError(err, cfg.NATSURL))
	}
	c.nats = nc
	js, err := jetstream.New(nc, cfg.Publish.JetStreamOptions()...)
	if err != nil {
		return fmt.Errorf("jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.js = js
	return nil
}

func (c *clients) close(ctx context.Context, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.CloseTimeout)
	defer cancel()
	var errs []error
	if c.nats != nil {
		c.nats.Close()
	}
	for _, rdb := range []*redis.Client{c.redis, c.slots} {
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
