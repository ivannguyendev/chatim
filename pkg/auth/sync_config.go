package auth

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

const (
	DefaultRevocationsEvery = 2 * time.Second
	DefaultKeysEvery        = time.Minute
	DefaultKeysMinGap       = 5 * time.Second
	DefaultReadyTimeout     = 10 * time.Second
	DefaultHTTPTimeout      = 2 * time.Second
	DefaultBodyMax          = 8 << 20
	DefaultSharedTTL        = time.Minute
	revocationsPath         = "/internal/v1/revocations"
)

type SyncConfig struct {
	InternalURL      string
	JWKSURL          string
	Secret           string
	RevocationsEvery time.Duration
	KeysEvery        time.Duration
	KeysMinGap       time.Duration
	ReadyTimeout     time.Duration
	HTTPTimeout      time.Duration
	BodyMax          int64
	SharedTTL        time.Duration
	AllowHTTP        bool
}

func (c SyncConfig) Validate() error {
	var errs []error
	switch {
	case c.InternalURL == "":
		errs = append(errs, errors.New("auth: internal url is required"))
	case !absolute(c.InternalURL, true):
		errs = append(errs, errors.New("auth: internal url must be an absolute http(s) url"))
	}
	if c.Secret == "" {
		errs = append(errs, errors.New("auth: node secret is required"))
	}
	switch {
	case c.JWKSURL == "":
		errs = append(errs, errors.New("auth: jwks url is required"))
	case !absolute(c.JWKSURL, c.AllowHTTP):
		errs = append(errs, errors.New("auth: jwks url must be an absolute https url (http only when allowed for dev)"))
	}
	if c.KeysEvery > MaxKeysInterval {
		errs = append(errs, fmt.Errorf("auth: keys interval must not exceed %v", MaxKeysInterval))
	}
	for name, d := range map[string]time.Duration{
		"revocations interval": c.RevocationsEvery, "keys interval": c.KeysEvery, "keys min gap": c.KeysMinGap,
		"ready timeout": c.ReadyTimeout, "http timeout": c.HTTPTimeout, "shared ttl": c.SharedTTL,
	} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("auth: %s must be positive", name))
		}
	}
	if c.BodyMax <= 0 {
		errs = append(errs, errors.New("auth: body max must be positive"))
	}
	return errors.Join(errs...)
}

func absolute(raw string, allowHTTP bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	return u.Scheme == "https" || (allowHTTP && u.Scheme == "http")
}

const (
	MaxEntryKeep    = 2 * time.Hour
	MaxRevokeAhead  = time.Minute
	MaxNodeLeeway   = time.Minute
	MaxKeysInterval = time.Minute
)

func NewNodeRevocationList(leeway time.Duration) (*RevocationList, error) {
	return NewRevocationList(RevocationListConfig{Leeway: leeway, MaxKeep: MaxEntryKeep, MaxAhead: MaxRevokeAhead})
}
