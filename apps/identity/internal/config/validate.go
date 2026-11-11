package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/core/envconfig"
)

const (
	kekLength           = 32
	minNodeSecretLength = 32
	shutdownMarginTime  = time.Second
	trustReloadCeiling  = time.Minute
)

type rule struct {
	ok  bool
	msg string
}

func (c Config) Validate() error {
	rules := []rule{
		{c.ListenAddr != "" || c.InternalAddr != "", "IDENTITY_ADDR and IDENTITY_INTERNAL_ADDR are both off; turn on at least one"},
		{c.AdminAddr != "", "IDENTITY_ADMIN_ADDR is required"},
		{c.ListenAddr != c.AdminAddr, "IDENTITY_ADMIN_ADDR must differ from IDENTITY_ADDR"},
		{c.InternalAddr == "" || (c.InternalAddr != c.AdminAddr && c.InternalAddr != c.ListenAddr), "IDENTITY_INTERNAL_ADDR must differ from IDENTITY_ADDR and IDENTITY_ADMIN_ADDR"},
		{c.InternalAddr == "" || len(c.NodeSecret) >= minNodeSecretLength, fmt.Sprintf("IDENTITY_NODE_SECRET must hold at least %d bytes when IDENTITY_INTERNAL_ADDR is on", minNodeSecretLength)},
		{c.KeyActivateLead >= c.KeyReload+auth.MaxKeysInterval, fmt.Sprintf("IDENTITY_KEY_ACTIVATE_LEAD %v must be at least IDENTITY_KEY_RELOAD + %v = %v so every node fetches a new key before it signs", c.KeyActivateLead, auth.MaxKeysInterval, c.KeyReload+auth.MaxKeysInterval)},
		{c.RevocationKeep() <= auth.MaxEntryKeep, fmt.Sprintf("IDENTITY_TOKEN_TTL %v + %v must not exceed %v, the longest revocation a node keeps", c.TokenTTL, revocationMargin, auth.MaxEntryKeep)},
		{c.MongoURI != "", "IDENTITY_MONGO_URI is required"},
		{!hasCredentials(c.MongoURI), "IDENTITY_MONGO_URI must not carry credentials; use IDENTITY_MONGO_USER and IDENTITY_MONGO_PASSWORD or IDENTITY_MONGO_PASSWORD_FILE"},
		{c.MongoPassword == "" || c.MongoUser != "", "IDENTITY_MONGO_PASSWORD needs IDENTITY_MONGO_USER"},
		{c.Issuer != "", "IDENTITY_ISSUER is required"},
		{c.Audience != "", "IDENTITY_AUDIENCE is required"},
		{c.ExchangeAudience != "", "IDENTITY_EXCHANGE_AUDIENCE is required"},
		{c.ExchangeAudience == "" || c.ExchangeAudience != c.Audience, "IDENTITY_EXCHANGE_AUDIENCE must differ from IDENTITY_AUDIENCE so a chat token is never taken as a tenant token"},
		{len(c.KeyEncryptionKey) == kekLength, fmt.Sprintf("IDENTITY_KEY_ENCRYPTION_KEY must decode to %d bytes", kekLength)},
		positive("IDENTITY_TOKEN_TTL", c.TokenTTL),
		positive("IDENTITY_EXCHANGE_MAX_LIFETIME", c.ExchangeMaxLifetime),
		positive("IDENTITY_TRUST_RELOAD", c.TrustReload),
		{c.TrustReload <= trustReloadCeiling, fmt.Sprintf("IDENTITY_TRUST_RELOAD %v must not exceed %v", c.TrustReload, trustReloadCeiling)},
		positive("IDENTITY_EXCHANGE_IP_RATE", c.ExchangeIPRate),
		positive("IDENTITY_EXCHANGE_IP_BURST", c.ExchangeIPBurst),
		positive("IDENTITY_EXCHANGE_IP_TRACKED", c.ExchangeIPTracked),
		positive("IDENTITY_KEY_RELOAD", c.KeyReload),
		positive(envconfig.BodySizeMaxKey, c.BodyMax),
		positive("IDENTITY_DRAIN_DELAY", c.DrainDelay),
		positive("IDENTITY_READ_HEADER_TIMEOUT", c.ReadHeaderTimeout),
		positive("IDENTITY_READ_TIMEOUT", c.ReadTimeout),
		positive("IDENTITY_WRITE_TIMEOUT", c.WriteTimeout),
		positive("IDENTITY_IDLE_TIMEOUT", c.IdleTimeout),
		positive("IDENTITY_MAX_HEADER_BYTES", c.MaxHeaderBytes),
		{c.ReadHeaderTimeout <= c.ReadTimeout, "IDENTITY_READ_HEADER_TIMEOUT must not exceed IDENTITY_READ_TIMEOUT"},
		positive("IDENTITY_USER_RATE", c.UserRate),
		positive("IDENTITY_USER_BURST", c.UserBurst),
		positive("IDENTITY_USER_INFLIGHT", c.UserInflight),
		positive("IDENTITY_USER_LIMITERS", c.UserLimiters),
		positive("IDENTITY_INFLIGHT_MAX", c.InflightMax),
		positive("IDENTITY_JIT_WRITERS", c.JITWriters),
		positive("IDENTITY_JIT_WINDOW", c.JITWindow),
		positive("IDENTITY_JIT_MAX_BATCH", c.JITMaxBatch),
		positive("IDENTITY_JIT_QUEUE", c.JITQueue),
		positive("IDENTITY_JIT_INSERT_TIMEOUT", c.JITInsertTimeout),
		{c.UserLimiters >= c.InflightMax, fmt.Sprintf("IDENTITY_USER_LIMITERS %d must be at least IDENTITY_INFLIGHT_MAX %d", c.UserLimiters, c.InflightMax)},
		{c.DirectoryLoadSlots >= 1, fmt.Sprintf("IDENTITY_DIRECTORY_LOAD_SLOTS %d must be at least 1", c.DirectoryLoadSlots)},
		{c.ShutdownBudget > c.DrainDelay+c.WriteTimeout+shutdownMarginTime, fmt.Sprintf("IDENTITY_SHUTDOWN_BUDGET %v must exceed IDENTITY_DRAIN_DELAY + IDENTITY_WRITE_TIMEOUT + 1s = %v", c.ShutdownBudget, c.DrainDelay+c.WriteTimeout+shutdownMarginTime)},
	}
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	for _, o := range c.CORSOrigins {
		if !validOrigin(o) {
			errs = append(errs, fmt.Errorf("IDENTITY_CORS_ORIGINS entry %q must be a scheme://host[:port] origin", o))
		}
	}
	return errors.Join(errs...)
}

func positive[T int | time.Duration](name string, v T) rule {
	return rule{v > 0, name + " must be positive"}
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && (u.Path == "" || u.Path == "/") && u.RawQuery == ""
}

func hasCredentials(raw string) bool {
	_, rest, ok := strings.Cut(raw, "://")
	authority, _, _ := strings.Cut(rest, "/")
	authority, _, _ = strings.Cut(authority, "?")
	return ok && strings.Contains(authority, "@")
}
