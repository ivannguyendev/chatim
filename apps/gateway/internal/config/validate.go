package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/core/envconfig"
)

type rule struct {
	ok  bool
	msg string
}

func (c Config) Validate() error {
	rules := []rule{
		{c.ListenAddr != "", "APP_ADDR is required"},
		{c.AdminAddr != "", "APP_ADMIN_ADDR is required"},
		{c.ListenAddr != c.AdminAddr, "APP_ADMIN_ADDR must differ from APP_ADDR"},
		{c.Redis.Addr != "", "REDIS_ADDR is required"},
		{c.Redis.DB >= 0, "REDIS_DB must not be negative"},
		{c.JWTKeysFile != "", "JWT_KEYS_FILE is required"},
		{c.JWTIssuer != "", "JWT_ISSUER is required"},
		{c.JWTAudience != "", "JWT_AUDIENCE is required"},
		{c.JWTLeeway >= 0, "JWT_LEEWAY must not be negative"},
		{c.JWTLeeway <= auth.MaxNodeLeeway, fmt.Sprintf("JWT_LEEWAY must not exceed %v, the extra time identity keeps a revocation", auth.MaxNodeLeeway)},
		{c.SharedRedis.DB >= 0, "SHARED_REDIS_DB must not be negative"},
		{c.SharedRedis.Addr == "" || c.SharedRedis.Timeout > 0, "SHARED_REDIS_TIMEOUT must be positive"},
		positive(envconfig.BodySizeMaxKey, c.BodyMax),
		positive("APP_USER_RATE", c.UserRate),
		positive("APP_USER_BURST", c.UserBurst),
		positive("APP_USER_INFLIGHT", c.UserInflight),
		positive("APP_USER_LIMITERS", c.UserLimiters),
		positive("APP_INFLIGHT_MAX", c.InflightMax),
		{c.UserLimiters >= c.InflightMax, fmt.Sprintf("APP_USER_LIMITERS %d must be at least APP_INFLIGHT_MAX %d", c.UserLimiters, c.InflightMax)},
		positive("APP_DRAIN_DELAY", c.DrainDelay),
		positive("APP_SHUTDOWN_BUDGET", c.ShutdownBudget),
		positive("CHAT_REQUEST_DEADLINE", c.RequestDeadline),
		{c.ShutdownBudget > c.DrainDelay+c.RequestDeadline, fmt.Sprintf("APP_SHUTDOWN_BUDGET %v must exceed APP_DRAIN_DELAY + CHAT_REQUEST_DEADLINE = %v", c.ShutdownBudget, c.DrainDelay+c.RequestDeadline)},
		positive("APP_READ_HEADER_TIMEOUT", c.ReadHeaderTimeout),
		positive("APP_READ_TIMEOUT", c.ReadTimeout),
		positive("APP_IDLE_TIMEOUT", c.IdleTimeout),
		positive("APP_MAX_HEADER_BYTES", c.MaxHeaderBytes),
		positive("GW_H2_MAX_STREAMS", c.H2MaxStreams),
		{c.ReadHeaderTimeout <= c.ReadTimeout, "APP_READ_HEADER_TIMEOUT must not exceed APP_READ_TIMEOUT"},
		{c.WriteTimeout >= c.RequestDeadline+writeTimeoutMargin, fmt.Sprintf("APP_WRITE_TIMEOUT %v must be at least CHAT_REQUEST_DEADLINE + %v = %v", c.WriteTimeout, writeTimeoutMargin, c.RequestDeadline+writeTimeoutMargin)},
	}
	errs := ruleErrors(rules)
	errs = append(errs, originErrors(c.CORSOrigins)...)
	if err := c.Identity.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func ruleErrors(rules []rule) []error {
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	return errs
}

func originErrors(origins []string) []error {
	var errs []error
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.String() != origin {
			errs = append(errs, fmt.Errorf("APP_CORS_ORIGINS entry %q must be an exact origin such as https://chat.example.com", origin))
		}
	}
	return errs
}

func positive[T int | time.Duration](key string, v T) rule {
	return rule{v > 0, key + " must be positive"}
}
