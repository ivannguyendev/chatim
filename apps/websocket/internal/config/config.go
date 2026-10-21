package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

const CloseTimeout = time.Second

type Config struct {
	NodeID           string
	ListenAddr       string
	AdminAddr        string
	RedisAddr        string
	RedisDB          int
	RedisPassword    string
	JWTKeysFile      string
	JWTIssuer        string
	JWTAudience      string
	JWTLeeway        time.Duration
	AuthTimeout      time.Duration
	UnauthMax        int
	FrameMax         int
	HeartbeatTimeout time.Duration
	CommandRate      int
	CommandBurst     int
	CommandInflight  int
	SendQueue        int
	WriteTimeout     time.Duration
	RequestDeadline  time.Duration
	DrainDelay       time.Duration
	ShutdownBudget   time.Duration
}

func Load() (Config, error) {
	var p parser
	c := Config{
		NodeID:           p.nodeID(),
		ListenAddr:       envconfig.String("WS_LISTEN", ":8080"),
		AdminAddr:        envconfig.String("ADMIN_LISTEN", ":9090"),
		RedisAddr:        envconfig.String("REDIS_ADDR", "chatim-redis:6379"),
		RedisDB:          p.number("REDIS_DB", 0),
		RedisPassword:    p.secret("REDIS_PASSWORD"),
		JWTKeysFile:      envconfig.String("WS_JWT_KEYS_FILE", ""),
		JWTIssuer:        envconfig.String("WS_JWT_ISSUER", ""),
		JWTAudience:      envconfig.String("WS_JWT_AUDIENCE", ""),
		JWTLeeway:        p.span("WS_JWT_LEEWAY", 30*time.Second),
		AuthTimeout:      p.span("WS_AUTH_TIMEOUT", 5*time.Second),
		UnauthMax:        p.number("WS_UNAUTH_MAX", 10000),
		FrameMax:         p.number("WS_FRAME_MAX", 128<<10),
		HeartbeatTimeout: p.span("WS_HEARTBEAT_TIMEOUT", 15*time.Second),
		CommandRate:      p.number("WS_COMMAND_RATE", 50),
		CommandBurst:     p.number("WS_COMMAND_BURST", 100),
		CommandInflight:  p.number("WS_COMMAND_INFLIGHT", 32),
		SendQueue:        p.number("WS_SEND_QUEUE", 256),
		WriteTimeout:     p.span("WS_WRITE_TIMEOUT", 10*time.Second),
		RequestDeadline:  p.span("CORE_REQUEST_DEADLINE", 5*time.Second),
		DrainDelay:       p.span("WS_DRAIN_DELAY", 2*time.Second),
		ShutdownBudget:   p.span("WS_SHUTDOWN_BUDGET", 15*time.Second),
	}
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("node_id", c.NodeID),
		slog.String("listen_addr", c.ListenAddr),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("redis_addr", c.RedisAddr),
		slog.Int("redis_db", c.RedisDB),
		slog.Bool("redis_auth", c.RedisPassword != ""),
		slog.String("jwt_keys_file", c.JWTKeysFile),
		slog.String("jwt_issuer", c.JWTIssuer),
		slog.String("jwt_audience", c.JWTAudience),
		slog.Duration("jwt_leeway", c.JWTLeeway),
		slog.Duration("auth_timeout", c.AuthTimeout),
		slog.Int("unauth_max", c.UnauthMax),
		slog.Int("frame_max", c.FrameMax),
		slog.Duration("heartbeat_timeout", c.HeartbeatTimeout),
		slog.Int("command_rate", c.CommandRate),
		slog.Int("command_burst", c.CommandBurst),
		slog.Int("command_inflight", c.CommandInflight),
		slog.Int("send_queue", c.SendQueue),
		slog.Duration("write_timeout", c.WriteTimeout),
		slog.Duration("request_deadline", c.RequestDeadline),
		slog.Duration("drain_delay", c.DrainDelay),
		slog.Duration("shutdown_budget", c.ShutdownBudget),
	)
}

func (c Config) String() string { return c.LogValue().String() }

type parser struct {
	errs []error
}

func (p *parser) fail(err error) { p.errs = append(p.errs, err) }

func (p *parser) nodeID() string {
	if id := os.Getenv("WS_NODE_ID"); id != "" {
		return id
	}
	host, err := os.Hostname()
	if err != nil {
		p.fail(fmt.Errorf("WS_NODE_ID unset and hostname unavailable: %w", err))
	}
	return host
}

func (p *parser) number(key string, def int) int {
	v, err := envconfig.Int(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}

func (p *parser) span(key string, def time.Duration) time.Duration {
	v, err := envconfig.Duration(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}

func (p *parser) secret(key string) string {
	v, err := envconfig.Secret(key)
	if err != nil {
		p.fail(err)
	}
	return v
}
