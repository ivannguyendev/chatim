package dedupe

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	DefaultPendingTTL   = 10 * time.Second
	DefaultCommittedTTL = 15 * time.Minute
	DefaultTimeout      = 100 * time.Millisecond
	DefaultCooldown     = time.Second

	maxNodeIDLen = 128
)

var ErrDegraded = fmt.Errorf("cid dedupe cooling down after a redis failure: %w", apperr.ErrUnavailable)

type Config struct {
	NodeID       string
	PendingTTL   time.Duration
	CommittedTTL time.Duration
	Timeout      time.Duration
	Cooldown     time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.PendingTTL = cmp.Or(c.PendingTTL, DefaultPendingTTL)
	c.CommittedTTL = cmp.Or(c.CommittedTTL, DefaultCommittedTTL)
	c.Timeout = cmp.Or(c.Timeout, DefaultTimeout)
	c.Cooldown = cmp.Or(c.Cooldown, DefaultCooldown)
	return c
}

func (c Config) validate() error {
	switch {
	case !validNodeID(c.NodeID):
		return fmt.Errorf("%w: dedupe node id %q must be 1-%d printable characters without ':' or spaces", apperr.ErrInvalidArgument, c.NodeID, maxNodeIDLen)
	case c.Timeout <= 0 || c.Cooldown <= 0:
		return fmt.Errorf("%w: dedupe timeout %v and cooldown %v must be positive", apperr.ErrInvalidArgument, c.Timeout, c.Cooldown)
	case c.PendingTTL < time.Millisecond || c.PendingTTL <= c.Timeout:
		return fmt.Errorf("%w: dedupe pending ttl %v must be at least 1ms and exceed the call timeout %v", apperr.ErrInvalidArgument, c.PendingTTL, c.Timeout)
	case c.CommittedTTL < c.PendingTTL:
		return fmt.Errorf("%w: dedupe committed ttl %v must not be shorter than pending ttl %v", apperr.ErrInvalidArgument, c.CommittedTTL, c.PendingTTL)
	default:
		return nil
	}
}

func validNodeID(id string) bool {
	if id == "" || len(id) > maxNodeIDLen {
		return false
	}
	for i := range len(id) {
		if id[i] <= ' ' || id[i] > '~' {
			return false
		}
	}
	return !strings.Contains(id, ":")
}
