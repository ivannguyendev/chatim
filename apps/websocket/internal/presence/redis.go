package presence

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const (
	keyPrefix          = "chatim:presence:"
	onlineValue        = "1"
	clientNamePrefix   = "chatim-websocket-presence-"
	redisMaxRetries    = 1
	redisDialerRetries = 1
)

var (
	errNoConn    = errors.New("presence: redis connection is required")
	errNoTimeout = errors.New("presence: redis timeout must be positive")
)

type RedisConfig struct {
	Addr     string
	DB       int
	Password string
	Pool     int
	Timeout  time.Duration
	Node     string
}

func RedisOptions(cfg RedisConfig) *redis.Options {
	return &redis.Options{
		Addr:                  cfg.Addr,
		DB:                    cfg.DB,
		Password:              cfg.Password,
		ClientName:            clientNamePrefix + cfg.Node,
		PoolSize:              cfg.Pool,
		DialTimeout:           cfg.Timeout,
		ReadTimeout:           cfg.Timeout,
		WriteTimeout:          cfg.Timeout,
		MaxRetries:            redisMaxRetries,
		DialerRetries:         redisDialerRetries,
		DisableIdentity:       true,
		ContextTimeoutEnabled: true,
	}
}

type Conn interface {
	Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
	Close() error
}

type Client struct {
	conn    Conn
	timeout time.Duration
	up      atomic.Bool
}

func NewClient(conn Conn, timeout time.Duration) (*Client, error) {
	switch {
	case conn == nil:
		return nil, errNoConn
	case timeout <= 0:
		return nil, errNoTimeout
	}
	return &Client{conn: conn, timeout: timeout}, nil
}

func Key(tenant, user string) string { return keyPrefix + tenant + ":" + user }

func (c *Client) Stamp(ctx context.Context, keys []string, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, err := c.conn.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, key := range keys {
			p.Set(ctx, key, onlineValue, ttl)
		}
		return nil
	})
	c.up.Store(err == nil)
	return err
}

func (c *Client) Online(ctx context.Context, keys []string) ([]bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	values, err := c.conn.MGet(readCtx, keys...).Result()
	if ctx.Err() == nil {
		c.up.Store(err == nil)
	}
	if err != nil {
		return nil, err
	}
	online := make([]bool, len(values))
	for i, v := range values {
		online[i] = v != nil
	}
	return online, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) Sources() []metrics.Source {
	return []metrics.Source{{
		Name: "presence_redis_up", Help: "1 when the last presence Redis command succeeded, 0 after it failed or before any.", Gauge: true,
		Read: func() float64 {
			if c.up.Load() {
				return 1
			}
			return 0
		},
	}}
}
