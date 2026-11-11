package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/core/envconfig"
	"github.com/ivannguyendev/chatim/pkg/core/redact"
)

const (
	defaultAdminAddr = ":9090"
	defaultMongoDB   = "chatim_identity"
	revocationMargin = time.Minute
)

type Config struct {
	ListenAddr          string
	InternalAddr        string
	AdminAddr           string
	MongoURI            string
	MongoDB             string
	MongoUser           string
	MongoPassword       string
	MongoAuthSource     string
	Issuer              string
	Audience            string
	TokenTTL            time.Duration
	ExchangeAudience    string
	ExchangeMaxLifetime time.Duration
	TrustReload         time.Duration
	ExchangeIPRate      int
	ExchangeIPBurst     int
	ExchangeIPTracked   int
	KeyEncryptionKey    []byte
	NodeSecret          string
	KeyActivateLead     time.Duration
	KeyReload           time.Duration
	BodyMax             int
	CORSOrigins         []string
	ClientIPHeader      string
	DrainDelay          time.Duration
	ShutdownBudget      time.Duration
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	MaxHeaderBytes      int
	DirectoryLoadSlots  int
	InflightMax         int
	UserRate            int
	UserBurst           int
	UserInflight        int
	UserLimiters        int
	JITWriters          int
	JITWindow           time.Duration
	JITMaxBatch         int
	JITQueue            int
	JITInsertTimeout    time.Duration
}

func AdminAddr() string { return envconfig.String("IDENTITY_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	mongoDB := envconfig.String("IDENTITY_MONGO_DB", defaultMongoDB)
	c := Config{
		ListenAddr:          optional("IDENTITY_ADDR", ":8080"),
		InternalAddr:        optional("IDENTITY_INTERNAL_ADDR", ":8081"),
		AdminAddr:           AdminAddr(),
		MongoURI:            envconfig.String("IDENTITY_MONGO_URI", ""),
		MongoDB:             mongoDB,
		MongoUser:           envconfig.String("IDENTITY_MONGO_USER", ""),
		MongoPassword:       p.secret("IDENTITY_MONGO_PASSWORD"),
		MongoAuthSource:     envconfig.String("IDENTITY_MONGO_AUTH_SOURCE", mongoDB),
		Issuer:              envconfig.String("IDENTITY_ISSUER", ""),
		Audience:            envconfig.String("IDENTITY_AUDIENCE", ""),
		TokenTTL:            p.span("IDENTITY_TOKEN_TTL", 5*time.Minute),
		ExchangeAudience:    envconfig.String("IDENTITY_EXCHANGE_AUDIENCE", ""),
		ExchangeMaxLifetime: p.span("IDENTITY_EXCHANGE_MAX_LIFETIME", 10*time.Minute),
		TrustReload:         p.span("IDENTITY_TRUST_RELOAD", 10*time.Second),
		ExchangeIPRate:      p.number("IDENTITY_EXCHANGE_IP_RATE", 50),
		ExchangeIPBurst:     p.number("IDENTITY_EXCHANGE_IP_BURST", 500),
		ExchangeIPTracked:   p.number("IDENTITY_EXCHANGE_IP_TRACKED", 100000),
		KeyEncryptionKey:    p.key("IDENTITY_KEY_ENCRYPTION_KEY"),
		NodeSecret:          p.secret("IDENTITY_NODE_SECRET"),
		KeyActivateLead:     p.span("IDENTITY_KEY_ACTIVATE_LEAD", 2*time.Minute),
		KeyReload:           p.span("IDENTITY_KEY_RELOAD", 30*time.Second),
		BodyMax:             p.number(envconfig.BodySizeMaxKey, envconfig.DefaultBodySizeMax),
		CORSOrigins:         list("IDENTITY_CORS_ORIGINS"),
		ClientIPHeader:      envconfig.String("IDENTITY_CLIENT_IP_HEADER", ""),
		DrainDelay:          p.span("IDENTITY_DRAIN_DELAY", 5*time.Second),
		ShutdownBudget:      p.span("IDENTITY_SHUTDOWN_BUDGET", 25*time.Second),
		ReadHeaderTimeout:   p.span("IDENTITY_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:         p.span("IDENTITY_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:        p.span("IDENTITY_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:         p.span("IDENTITY_IDLE_TIMEOUT", 60*time.Second),
		MaxHeaderBytes:      p.number("IDENTITY_MAX_HEADER_BYTES", 16<<10),
		DirectoryLoadSlots:  p.number("IDENTITY_DIRECTORY_LOAD_SLOTS", 4),
		InflightMax:         p.number("IDENTITY_INFLIGHT_MAX", 10000),
		UserRate:            p.number("IDENTITY_USER_RATE", 50),
		UserBurst:           p.number("IDENTITY_USER_BURST", 100),
		UserInflight:        p.number("IDENTITY_USER_INFLIGHT", 32),
		UserLimiters:        p.number("IDENTITY_USER_LIMITERS", 100000),
		JITWriters:          p.number("IDENTITY_JIT_WRITERS", 4),
		JITWindow:           p.span("IDENTITY_JIT_WINDOW", 2*time.Millisecond),
		JITMaxBatch:         p.number("IDENTITY_JIT_MAX_BATCH", 256),
		JITQueue:            p.number("IDENTITY_JIT_QUEUE", 1024),
		JITInsertTimeout:    p.span("IDENTITY_JIT_INSERT_TIMEOUT", time.Second),
	}
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) RevocationKeep() time.Duration { return c.TokenTTL + revocationMargin }

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("listen_addr", c.ListenAddr),
		slog.String("internal_addr", c.InternalAddr),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("mongo_uri", redact.URL(c.MongoURI)),
		slog.String("mongo_db", c.MongoDB),
		slog.String("mongo_user", c.MongoUser),
		slog.Bool("mongo_password_set", c.MongoPassword != ""),
		slog.String("mongo_auth_source", c.MongoAuthSource),
		slog.String("issuer", c.Issuer),
		slog.String("audience", c.Audience),
		slog.Duration("token_ttl", c.TokenTTL),
		slog.String("exchange_audience", c.ExchangeAudience),
		slog.Duration("exchange_max_lifetime", c.ExchangeMaxLifetime),
		slog.Duration("trust_reload", c.TrustReload),
		slog.Int("exchange_ip_rate", c.ExchangeIPRate),
		slog.Int("exchange_ip_burst", c.ExchangeIPBurst),
		slog.Int("exchange_ip_tracked", c.ExchangeIPTracked),
		slog.Bool("key_encryption_key_set", len(c.KeyEncryptionKey) > 0),
		slog.Bool("node_secret_set", c.NodeSecret != ""),
		slog.Duration("key_activate_lead", c.KeyActivateLead),
		slog.Duration("key_reload", c.KeyReload),
		slog.Int("body_max", c.BodyMax),
		slog.String("cors_origins", strings.Join(c.CORSOrigins, ",")),
		slog.String("client_ip_header", c.ClientIPHeader),
		slog.Duration("drain_delay", c.DrainDelay),
		slog.Duration("shutdown_budget", c.ShutdownBudget),
		slog.Int("directory_load_slots", c.DirectoryLoadSlots),
		slog.Int("inflight_max", c.InflightMax),
		slog.Int("user_rate", c.UserRate),
		slog.Int("user_burst", c.UserBurst),
		slog.Int("user_inflight", c.UserInflight),
		slog.Int("user_limiters", c.UserLimiters),
		slog.Int("jit_writers", c.JITWriters),
		slog.Duration("jit_window", c.JITWindow),
		slog.Int("jit_max_batch", c.JITMaxBatch),
		slog.Int("jit_queue", c.JITQueue),
		slog.Duration("jit_insert_timeout", c.JITInsertTimeout),
	)
}

func (c Config) String() string { return c.LogValue().String() }

func optional(key, def string) string {
	v, set := os.LookupEnv(key)
	if !set {
		return def
	}
	return strings.TrimSpace(v)
}

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

func (p *parser) number(key string, def int) int {
	v, err := envconfig.Int(key, def)
	if err != nil {
		p.errs = append(p.errs, err)
	}
	return v
}

func (p *parser) span(key string, def time.Duration) time.Duration {
	v, err := envconfig.Duration(key, def)
	if err != nil {
		p.errs = append(p.errs, err)
	}
	return v
}

func (p *parser) secret(key string) string {
	v, err := envconfig.Secret(key)
	if err != nil {
		p.errs = append(p.errs, err)
	}
	return v
}

func (p *parser) key(name string) []byte {
	raw := p.secret(name)
	if raw == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be base64", name))
		return nil
	}
	return b
}
