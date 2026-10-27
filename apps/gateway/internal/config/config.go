package config

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

const (
	writeTimeoutMargin = 5 * time.Second
	defaultAdminAddr   = ":9090"
)

type Config struct {
	ListenAddr        string
	AdminAddr         string
	CoreAddrs         []string
	JWTKeysFile       string
	JWTIssuer         string
	JWTAudience       string
	JWTLeeway         time.Duration
	BodyMax           int
	UserRate          int
	UserBurst         int
	UserInflight      int
	UserLimiters      int
	InflightMax       int
	CORSOrigins       []string
	DrainDelay        time.Duration
	ShutdownBudget    time.Duration
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	H2MaxStreams      int
	RequestDeadline   time.Duration
	IAM               iamsync.Config
	SharedRedis       SharedRedis
}

type SharedRedis struct {
	Addr     string
	DB       int
	Password string
	Timeout  time.Duration
}

func AdminAddr() string { return envconfig.String("GW_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	deadline := p.span("CORE_REQUEST_DEADLINE", 5*time.Second)
	c := Config{
		ListenAddr:        envconfig.String("GW_ADDR", ":8080"),
		AdminAddr:         AdminAddr(),
		CoreAddrs:         list("GW_CORE_ADDRS"),
		JWTKeysFile:       envconfig.String("GW_JWT_KEYS_FILE", ""),
		JWTIssuer:         envconfig.String("GW_JWT_ISSUER", ""),
		JWTAudience:       envconfig.String("GW_JWT_AUDIENCE", ""),
		JWTLeeway:         p.span("GW_JWT_LEEWAY", 30*time.Second),
		BodyMax:           p.number("GW_BODY_MAX", 128<<10),
		UserRate:          p.number("GW_USER_RATE", 50),
		UserBurst:         p.number("GW_USER_BURST", 100),
		UserInflight:      p.number("GW_USER_INFLIGHT", 32),
		UserLimiters:      p.number("GW_USER_LIMITERS", 100000),
		InflightMax:       p.number("GW_INFLIGHT_MAX", 10000),
		CORSOrigins:       list("GW_CORS_ORIGINS"),
		DrainDelay:        p.span("GW_DRAIN_DELAY", 2*time.Second),
		ShutdownBudget:    p.span("GW_SHUTDOWN_BUDGET", 15*time.Second),
		ReadHeaderTimeout: p.span("GW_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       p.span("GW_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      p.span("GW_WRITE_TIMEOUT", deadline+writeTimeoutMargin),
		IdleTimeout:       p.span("GW_IDLE_TIMEOUT", 60*time.Second),
		MaxHeaderBytes:    p.number("GW_MAX_HEADER_BYTES", 16<<10),
		H2MaxStreams:      p.number("GW_H2_MAX_STREAMS", 250),
		RequestDeadline:   deadline,
		SharedRedis: SharedRedis{
			Addr:     envconfig.String("GW_SHARED_REDIS_ADDR", ""),
			DB:       p.number("GW_SHARED_REDIS_DB", 0),
			Password: p.secret("GW_SHARED_REDIS_PASSWORD"),
			Timeout:  p.span("GW_SHARED_REDIS_TIMEOUT", 500*time.Millisecond),
		},
	}
	iam, err := iamsync.FromEnv("GW")
	if err != nil {
		p.fail(err)
	}
	c.IAM = iam
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
		slog.String("listen_addr", c.ListenAddr),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("core_addrs", strings.Join(c.CoreAddrs, ",")),
		slog.String("jwt_keys_file", c.JWTKeysFile),
		slog.String("jwt_issuer", c.JWTIssuer),
		slog.String("jwt_audience", c.JWTAudience),
		slog.Duration("jwt_leeway", c.JWTLeeway),
		slog.Int("body_max", c.BodyMax),
		slog.Int("user_rate", c.UserRate),
		slog.Int("user_burst", c.UserBurst),
		slog.Int("user_inflight", c.UserInflight),
		slog.Int("user_limiters", c.UserLimiters),
		slog.Int("inflight_max", c.InflightMax),
		slog.String("cors_origins", strings.Join(c.CORSOrigins, ",")),
		slog.Duration("drain_delay", c.DrainDelay),
		slog.Duration("shutdown_budget", c.ShutdownBudget),
		slog.Duration("read_header_timeout", c.ReadHeaderTimeout),
		slog.Duration("read_timeout", c.ReadTimeout),
		slog.Duration("write_timeout", c.WriteTimeout),
		slog.Duration("idle_timeout", c.IdleTimeout),
		slog.Int("max_header_bytes", c.MaxHeaderBytes),
		slog.Int("h2_max_streams", c.H2MaxStreams),
		slog.Duration("request_deadline", c.RequestDeadline),
		slog.Any("iam", c.IAM),
		slog.Group("shared_redis", slog.String("addr", c.SharedRedis.Addr), slog.Int("db", c.SharedRedis.DB),
			slog.Bool("auth", c.SharedRedis.Password != ""), slog.Duration("timeout", c.SharedRedis.Timeout)),
	)
}

func (c Config) String() string { return c.LogValue().String() }

func list(key string) []string {
	var out []string
	for item := range strings.SplitSeq(os.Getenv(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

type parser struct {
	errs []error
}

func (p *parser) fail(err error) { p.errs = append(p.errs, err) }

func (p *parser) number(key string, def int) int {
	v, err := envconfig.Int(key, def)
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

func (p *parser) span(key string, def time.Duration) time.Duration {
	v, err := envconfig.Duration(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}
