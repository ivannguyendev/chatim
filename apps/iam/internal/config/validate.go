package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

const (
	kekLength          = 32
	minPasswordLength  = 8
	shutdownMarginTime = time.Second
)

type rule struct {
	ok  bool
	msg string
}

func (c Config) Validate() error {
	rules := []rule{
		{c.ListenAddr != "", "IAM_ADDR is required"},
		{c.AdminAddr != "", "IAM_ADMIN_ADDR is required"},
		{c.ListenAddr != c.AdminAddr, "IAM_ADMIN_ADDR must differ from IAM_ADDR"},
		{validBaseURL(c.PublicURL), "IAM_PUBLIC_URL must be an absolute http(s) url without query or fragment"},
		{c.PGURI != "", "IAM_PG_URI is required"},
		{!hasPassword(c.PGURI), "IAM_PG_URI must not carry a password; use IAM_PG_PASSWORD or IAM_PG_PASSWORD_FILE"},
		{c.Issuer != "", "IAM_ISSUER is required"},
		{c.Audience != "", "IAM_AUDIENCE is required"},
		{len(c.KeyEncryptionKey) == kekLength, fmt.Sprintf("IAM_KEY_ENCRYPTION_KEY must decode to %d bytes", kekLength)},
		positive("IAM_PG_MAX_CONNS", c.PGMaxConns),
		positive("IAM_ACCESS_TTL", c.AccessTTL),
		positive("IAM_REFRESH_GRACE", c.RefreshGrace),
		positive("IAM_DPOP_WINDOW", c.DPoPWindow),
		{c.RefreshGrace < c.AccessTTL, "IAM_REFRESH_GRACE must be shorter than IAM_ACCESS_TTL"},
		{c.AccessTTL < c.SessionIdle, "IAM_ACCESS_TTL must be shorter than IAM_SESSION_IDLE"},
		{c.SessionIdle <= c.SessionMax, "IAM_SESSION_IDLE must not exceed IAM_SESSION_MAX"},
		positive("IAM_HASH_CONCURRENCY", c.HashConcurrency),
		{c.LoginFreeFailures >= 0, "IAM_LOGIN_FREE_FAILURES must not be negative"},
		positive("IAM_LOGIN_MAX_DELAY", c.LoginMaxDelay),
		positive("IAM_LOGIN_IP_RATE", c.LoginIPRate),
		positive("IAM_LOGIN_IP_BURST", c.LoginIPBurst),
		positive("IAM_LOGIN_IP_TRACKED", c.LoginIPTracked),
		{c.PasswordMin >= minPasswordLength, fmt.Sprintf("IAM_PASSWORD_MIN must be at least %d", minPasswordLength)},
		positive("IAM_KEY_RELOAD", c.KeyReload),
		positive("IAM_BODY_MAX", c.BodyMax),
		positive("IAM_DRAIN_DELAY", c.DrainDelay),
		positive("IAM_READ_HEADER_TIMEOUT", c.ReadHeaderTimeout),
		positive("IAM_READ_TIMEOUT", c.ReadTimeout),
		positive("IAM_WRITE_TIMEOUT", c.WriteTimeout),
		positive("IAM_IDLE_TIMEOUT", c.IdleTimeout),
		positive("IAM_MAX_HEADER_BYTES", c.MaxHeaderBytes),
		{c.ReadHeaderTimeout <= c.ReadTimeout, "IAM_READ_HEADER_TIMEOUT must not exceed IAM_READ_TIMEOUT"},
		{c.ShutdownBudget > c.DrainDelay+c.WriteTimeout+shutdownMarginTime, fmt.Sprintf("IAM_SHUTDOWN_BUDGET %v must exceed IAM_DRAIN_DELAY + IAM_WRITE_TIMEOUT + 1s = %v", c.ShutdownBudget, c.DrainDelay+c.WriteTimeout+shutdownMarginTime)},
	}
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	for _, o := range c.CORSOrigins {
		if !validOrigin(o) {
			errs = append(errs, fmt.Errorf("IAM_CORS_ORIGINS entry %q must be a scheme://host[:port] origin", o))
		}
	}
	return errors.Join(errs...)
}

func positive[T int | time.Duration](name string, v T) rule {
	return rule{v > 0, name + " must be positive"}
}

func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.RawQuery == "" && u.Fragment == ""
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && (u.Path == "" || u.Path == "/") && u.RawQuery == ""
}

func hasPassword(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return false
	}
	_, set := u.User.Password()
	return set
}
