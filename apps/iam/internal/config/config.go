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
	PublicURL          string
	PGURI              string
	PGPassword         string
	PGMaxConns         int
	Issuer             string
	Audience           string
	AccessTTL          time.Duration
	SessionIdle        time.Duration
	SessionMax         time.Duration
	RefreshGrace       time.Duration
	DPoPWindow         time.Duration
	HashConcurrency    int
	LoginFreeFailures  int
	LoginMaxDelay      time.Duration
	LoginIPRate        int
	LoginIPBurst       int
	LoginIPTracked     int
	PasswordMin        int
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

func AdminAddr() string { return envconfig.String("IAM_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	c := Config{
		ListenAddr:         optional("IAM_ADDR", ":8080"),
		InternalAddr:       optional("IAM_INTERNAL_ADDR", ":8081"),
		AdminAddr:          AdminAddr(),
		PublicURL:          envconfig.String("IAM_PUBLIC_URL", ""),
		PGURI:              envconfig.String("IAM_PG_URI", ""),
		PGPassword:         p.secret("IAM_PG_PASSWORD"),
		PGMaxConns:         p.number("IAM_PG_MAX_CONNS", 20),
		Issuer:             envconfig.String("IAM_ISSUER", ""),
		Audience:           envconfig.String("IAM_AUDIENCE", ""),
		AccessTTL:          p.span("IAM_ACCESS_TTL", 15*time.Minute),
		SessionIdle:        p.span("IAM_SESSION_IDLE", 720*time.Hour),
		SessionMax:         p.span("IAM_SESSION_MAX", 2160*time.Hour),
		RefreshGrace:       p.span("IAM_REFRESH_GRACE", 30*time.Second),
		DPoPWindow:         p.span("IAM_DPOP_WINDOW", time.Minute),
		HashConcurrency:    p.number("IAM_HASH_CONCURRENCY", 16),
		LoginFreeFailures:  p.number("IAM_LOGIN_FREE_FAILURES", 5),
		LoginMaxDelay:      p.span("IAM_LOGIN_MAX_DELAY", time.Minute),
		LoginIPRate:        p.number("IAM_LOGIN_IP_RATE", 20),
		LoginIPBurst:       p.number("IAM_LOGIN_IP_BURST", 200),
		LoginIPTracked:     p.number("IAM_LOGIN_IP_TRACKED", 100000),
		PasswordMin:        p.number("IAM_PASSWORD_MIN", 12),
		KeyEncryptionKey:   p.key("IAM_KEY_ENCRYPTION_KEY"),
		NodeSecret:         p.secret("IAM_NODE_SECRET"),
		KeyActivateLead:    p.span("IAM_KEY_ACTIVATE_LEAD", 2*time.Minute),
		KeyReload:          p.span("IAM_KEY_RELOAD", 30*time.Second),
		BodyMax:            p.number("IAM_BODY_MAX", 16<<10),
		CORSOrigins:        list("IAM_CORS_ORIGINS"),
		ClientIPHeader:     envconfig.String("IAM_CLIENT_IP_HEADER", ""),
		DrainDelay:         p.span("IAM_DRAIN_DELAY", 5*time.Second),
		ShutdownBudget:     p.span("IAM_SHUTDOWN_BUDGET", 25*time.Second),
		ReadHeaderTimeout:  p.span("IAM_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:        p.span("IAM_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:       p.span("IAM_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:        p.span("IAM_IDLE_TIMEOUT", 60*time.Second),
		MaxHeaderBytes:     p.number("IAM_MAX_HEADER_BYTES", 16<<10),
		FeedWaitMax:        p.span("IAM_FEED_WAIT_MAX", 10*time.Second),
		DirectoryLoadSlots: p.number("IAM_DIRECTORY_LOAD_SLOTS", 4),
		InflightMax:        p.number("IAM_INFLIGHT_MAX", 10000),
		UserRate:           p.number("IAM_USER_RATE", 50),
		UserBurst:          p.number("IAM_USER_BURST", 100),
		UserInflight:       p.number("IAM_USER_INFLIGHT", 32),
		UserLimiters:       p.number("IAM_USER_LIMITERS", 100000),
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
		slog.String("public_url", c.PublicURL),
		slog.String("pg_uri", redact.URL(c.PGURI)),
		slog.Bool("pg_password_set", c.PGPassword != ""),
		slog.Int("pg_max_conns", c.PGMaxConns),
		slog.String("issuer", c.Issuer),
		slog.String("audience", c.Audience),
		slog.Duration("access_ttl", c.AccessTTL),
		slog.Duration("session_idle", c.SessionIdle),
		slog.Duration("session_max", c.SessionMax),
		slog.Duration("refresh_grace", c.RefreshGrace),
		slog.Duration("dpop_window", c.DPoPWindow),
		slog.Int("hash_concurrency", c.HashConcurrency),
		slog.Int("login_free_failures", c.LoginFreeFailures),
		slog.Duration("login_max_delay", c.LoginMaxDelay),
		slog.Int("login_ip_rate", c.LoginIPRate),
		slog.Int("login_ip_burst", c.LoginIPBurst),
		slog.Int("password_min", c.PasswordMin),
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
