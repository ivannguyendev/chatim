package iamsync

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ivannguyendev/chatim/pkg/revocation"
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

type Config struct {
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

func (c Config) Validate() error {
	var errs []error
	switch {
	case c.InternalURL == "":
		errs = append(errs, errors.New("iamsync: internal url is required"))
	case !absolute(c.InternalURL, true):
		errs = append(errs, errors.New("iamsync: internal url must be an absolute http(s) url"))
	}
	if c.Secret == "" {
		errs = append(errs, errors.New("iamsync: node secret is required"))
	}
	switch {
	case c.JWKSURL == "":
		errs = append(errs, errors.New("iamsync: jwks url is required"))
	case !absolute(c.JWKSURL, c.AllowHTTP):
		errs = append(errs, errors.New("iamsync: jwks url must be an absolute https url (http only when allowed for dev)"))
	}
	if c.KeysEvery > MaxKeysInterval {
		errs = append(errs, fmt.Errorf("iamsync: keys interval must not exceed %v", MaxKeysInterval))
	}
	for name, d := range map[string]time.Duration{
		"revocations interval": c.RevocationsEvery, "keys interval": c.KeysEvery, "keys min gap": c.KeysMinGap,
		"ready timeout": c.ReadyTimeout, "http timeout": c.HTTPTimeout, "shared ttl": c.SharedTTL,
	} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("iamsync: %s must be positive", name))
		}
	}
	if c.BodyMax <= 0 {
		errs = append(errs, errors.New("iamsync: body max must be positive"))
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

func NewList(leeway time.Duration) (*revocation.List, error) {
	return revocation.New(revocation.Config{Leeway: leeway, MaxKeep: MaxEntryKeep, MaxAhead: MaxRevokeAhead})
}
