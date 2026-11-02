package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const (
	defaultAdminAddr = ":9090"
	revocationMargin = time.Minute
)

type Config struct {
	ListenAddr         string
	InternalAddr       string
	AdminAddr          string
	PGURI              string
	PGPassword         string
	PGMaxConns         int
	Issuer             string
	Audience           string
	AccessTTL          time.Duration
	KeyEncryptionKey   []byte
	NodeSecret         string
	KeyActivateLead    time.Duration
	KeyReload          time.Duration
	BodyMax            int
	CORSOrigins        []string
	ClientIPHeader     string
	DrainDelay         time.Duration
	ShutdownBudget     time.Duration
	ReadHeaderTimeout  time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	MaxHeaderBytes     int
	FeedWaitMax        time.Duration
	DirectoryLoadSlots int
	InflightMax        int
	UserRate           int
	UserBurst          int
	UserInflight       int
	UserLimiters       int
}

func AdminAddr() string { return envconfig.String("IDENTITY_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	c := Config{
		ListenAddr:         optional("IDENTITY_ADDR", ":8080"),
		InternalAddr:       optional("IDENTITY_INTERNAL_ADDR", ":8081"),
		AdminAddr:          AdminAddr(),
		PGURI:              envconfig.String("IDENTITY_PG_URI", ""),
		PGPassword:         p.secret("IDENTITY_PG_PASSWORD"),
		PGMaxConns:         p.number("IDENTITY_PG_MAX_CONNS", 20),
		Issuer:             envconfig.String("IDENTITY_ISSUER", ""),
		Audience:           envconfig.String("IDENTITY_AUDIENCE", ""),
		AccessTTL:          p.span("IDENTITY_ACCESS_TTL", 15*time.Minute),
		KeyEncryptionKey:   p.key("IDENTITY_KEY_ENCRYPTION_KEY"),
		NodeSecret:         p.secret("IDENTITY_NODE_SECRET"),
		KeyActivateLead:    p.span("IDENTITY_KEY_ACTIVATE_LEAD", 2*time.Minute),
		KeyReload:          p.span("IDENTITY_KEY_RELOAD", 30*time.Second),
		BodyMax:            p.number("IDENTITY_BODY_MAX", 16<<10),
		CORSOrigins:        list("IDENTITY_CORS_ORIGINS"),
		ClientIPHeader:     envconfig.String("IDENTITY_CLIENT_IP_HEADER", ""),
		DrainDelay:         p.span("IDENTITY_DRAIN_DELAY", 5*time.Second),
		ShutdownBudget:     p.span("IDENTITY_SHUTDOWN_BUDGET", 25*time.Second),
		ReadHeaderTimeout:  p.span("IDENTITY_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:        p.span("IDENTITY_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:       p.span("IDENTITY_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:        p.span("IDENTITY_IDLE_TIMEOUT", 60*time.Second),
		MaxHeaderBytes:     p.number("IDENTITY_MAX_HEADER_BYTES", 16<<10),
		FeedWaitMax:        p.span("IDENTITY_FEED_WAIT_MAX", 10*time.Second),
		DirectoryLoadSlots: p.number("IDENTITY_DIRECTORY_LOAD_SLOTS", 4),
		InflightMax:        p.number("IDENTITY_INFLIGHT_MAX", 10000),
		UserRate:           p.number("IDENTITY_USER_RATE", 50),
		UserBurst:          p.number("IDENTITY_USER_BURST", 100),
		UserInflight:       p.number("IDENTITY_USER_INFLIGHT", 32),
		UserLimiters:       p.number("IDENTITY_USER_LIMITERS", 100000),
	}
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) RevocationKeep() time.Duration { return c.AccessTTL + revocationMargin }

func (c Config) DatabaseURI() string {
	u, err := url.Parse(c.PGURI)
	if err != nil || u.User == nil {
		return c.PGURI
	}
	u.User = url.UserPassword(u.User.Username(), c.PGPassword)
	return u.String()
}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("listen_addr", c.ListenAddr),
		slog.String("internal_addr", c.InternalAddr),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("pg_uri", redact.URL(c.PGURI)),
		slog.Bool("pg_password_set", c.PGPassword != ""),
		slog.Int("pg_max_conns", c.PGMaxConns),
		slog.String("issuer", c.Issuer),
		slog.String("audience", c.Audience),
		slog.Duration("access_ttl", c.AccessTTL),
		slog.Bool("key_encryption_key_set", len(c.KeyEncryptionKey) > 0),
		slog.Bool("node_secret_set", c.NodeSecret != ""),
		slog.Duration("key_activate_lead", c.KeyActivateLead),
		slog.Duration("key_reload", c.KeyReload),
		slog.Int("body_max", c.BodyMax),
		slog.String("cors_origins", strings.Join(c.CORSOrigins, ",")),
		slog.String("client_ip_header", c.ClientIPHeader),
		slog.Duration("drain_delay", c.DrainDelay),
		slog.Duration("shutdown_budget", c.ShutdownBudget),
		slog.Duration("feed_wait_max", c.FeedWaitMax),
		slog.Int("directory_load_slots", c.DirectoryLoadSlots),
		slog.Int("inflight_max", c.InflightMax),
		slog.Int("user_rate", c.UserRate),
		slog.Int("user_burst", c.UserBurst),
		slog.Int("user_inflight", c.UserInflight),
		slog.Int("user_limiters", c.UserLimiters),
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
