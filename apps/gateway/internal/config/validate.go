package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"time"
)

type rule struct {
	ok  bool
	msg string
}

func (c Config) Validate() error {
	rules := []rule{
		{c.ListenAddr != "", "GW_ADDR is required"},
		{c.AdminAddr != "", "GW_ADMIN_ADDR is required"},
		{c.ListenAddr != c.AdminAddr, "GW_ADMIN_ADDR must differ from GW_ADDR"},
		{len(c.CoreAddrs) > 0, "GW_CORE_ADDRS is required"},
		{c.JWTKeysFile != "", "GW_JWT_KEYS_FILE is required"},
		{c.JWTIssuer != "", "GW_JWT_ISSUER is required"},
		{c.JWTAudience != "", "GW_JWT_AUDIENCE is required"},
		{c.JWTLeeway >= 0, "GW_JWT_LEEWAY must not be negative"},
		positive("GW_BODY_MAX", c.BodyMax),
		positive("GW_USER_RATE", c.UserRate),
		positive("GW_USER_BURST", c.UserBurst),
		positive("GW_USER_INFLIGHT", c.UserInflight),
		positive("GW_USER_LIMITERS", c.UserLimiters),
		positive("GW_INFLIGHT_MAX", c.InflightMax),
		{c.UserLimiters >= c.InflightMax, fmt.Sprintf("GW_USER_LIMITERS %d must be at least GW_INFLIGHT_MAX %d", c.UserLimiters, c.InflightMax)},
		positive("GW_DRAIN_DELAY", c.DrainDelay),
		positive("GW_SHUTDOWN_BUDGET", c.ShutdownBudget),
		positive("CORE_REQUEST_DEADLINE", c.RequestDeadline),
		{c.ShutdownBudget > c.DrainDelay+c.RequestDeadline, fmt.Sprintf("GW_SHUTDOWN_BUDGET %v must exceed GW_DRAIN_DELAY + CORE_REQUEST_DEADLINE = %v", c.ShutdownBudget, c.DrainDelay+c.RequestDeadline)},
		positive("GW_READ_HEADER_TIMEOUT", c.ReadHeaderTimeout),
		positive("GW_READ_TIMEOUT", c.ReadTimeout),
		positive("GW_IDLE_TIMEOUT", c.IdleTimeout),
		positive("GW_MAX_HEADER_BYTES", c.MaxHeaderBytes),
		positive("GW_H2_MAX_STREAMS", c.H2MaxStreams),
		{c.ReadHeaderTimeout <= c.ReadTimeout, "GW_READ_HEADER_TIMEOUT must not exceed GW_READ_TIMEOUT"},
		{c.WriteTimeout >= c.RequestDeadline+writeTimeoutMargin, fmt.Sprintf("GW_WRITE_TIMEOUT %v must be at least CORE_REQUEST_DEADLINE + %v = %v", c.WriteTimeout, writeTimeoutMargin, c.RequestDeadline+writeTimeoutMargin)},
	}
	errs := ruleErrors(rules)
	errs = append(errs, coreAddrErrors(c.CoreAddrs)...)
	errs = append(errs, originErrors(c.CORSOrigins)...)
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

func coreAddrErrors(addrs []string) []error {
	var errs []error
	for i, addr := range addrs {
		host, port, err := net.SplitHostPort(addr)
		switch {
		case err != nil || host == "" || port == "":
			errs = append(errs, fmt.Errorf("GW_CORE_ADDRS entry %q must be host:port", addr))
		case slices.Contains(addrs[:i], addr):
			errs = append(errs, fmt.Errorf("GW_CORE_ADDRS lists %q twice", addr))
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
			errs = append(errs, fmt.Errorf("GW_CORS_ORIGINS entry %q must be an exact origin such as https://chat.example.com", origin))
		}
	}
	return errs
}

func positive[T int | time.Duration](key string, v T) rule {
	return rule{v > 0, key + " must be positive"}
}
