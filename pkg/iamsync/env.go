package iamsync

import (
	"errors"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

func FromEnv(prefix string) (Config, error) {
	key := func(name string) string { return prefix + "_IAM_" + name }
	var errs []error
	span := func(name string, def time.Duration) time.Duration {
		v, err := envconfig.Duration(key(name), def)
		if err != nil {
			errs = append(errs, err)
		}
		return v
	}
	secret, err := envconfig.Secret(key("SECRET"))
	if err != nil {
		errs = append(errs, err)
	}
	allowHTTP, err := envconfig.Bool(key("ALLOW_HTTP"), false)
	if err != nil {
		errs = append(errs, err)
	}
	c := Defaults()
	c.InternalURL = envconfig.String(key("INTERNAL_URL"), "")
	c.JWKSURL = envconfig.String(key("JWKS_URL"), "")
	c.Secret = secret
	c.RevocationsEvery = span("REVOCATIONS_INTERVAL", DefaultRevocationsEvery)
	c.KeysEvery = span("KEYS_INTERVAL", DefaultKeysEvery)
	c.ReadyTimeout = span("READY_TIMEOUT", DefaultReadyTimeout)
	c.AllowHTTP = allowHTTP
	return c, errors.Join(errs...)
}

func Defaults() Config {
	return Config{
		RevocationsEvery: DefaultRevocationsEvery,
		KeysEvery:        DefaultKeysEvery,
		KeysMinGap:       DefaultKeysMinGap,
		ReadyTimeout:     DefaultReadyTimeout,
		HTTPTimeout:      DefaultHTTPTimeout,
		BodyMax:          DefaultBodyMax,
		SharedTTL:        DefaultSharedTTL,
	}
}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("internal_url", c.InternalURL),
		slog.String("jwks_url", c.JWKSURL),
		slog.Bool("secret_set", c.Secret != ""),
		slog.Duration("revocations_interval", c.RevocationsEvery),
		slog.Duration("keys_interval", c.KeysEvery),
		slog.Duration("ready_timeout", c.ReadyTimeout),
		slog.Bool("allow_http", c.AllowHTTP),
	)
}
