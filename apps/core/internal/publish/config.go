package publish

import (
	"cmp"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultShards       = 4
	DefaultQueueSize    = 1024
	DefaultMaxPending   = 256
	DefaultAttempts     = 4
	DefaultRetryBackoff = 100 * time.Millisecond
	DefaultAckTimeout   = 2 * time.Second
)

type Config struct {
	SubjectRoot  string
	Shards       int
	QueueSize    int
	MaxPending   int
	Attempts     int
	RetryBackoff time.Duration
	AckTimeout   time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Shards = cmp.Or(c.Shards, DefaultShards)
	c.QueueSize = cmp.Or(c.QueueSize, DefaultQueueSize)
	c.MaxPending = cmp.Or(c.MaxPending, DefaultMaxPending)
	c.Attempts = cmp.Or(c.Attempts, DefaultAttempts)
	c.RetryBackoff = cmp.Or(c.RetryBackoff, DefaultRetryBackoff)
	c.AckTimeout = cmp.Or(c.AckTimeout, DefaultAckTimeout)
	return c
}

func (c Config) validate() error {
	counts := []int{c.Shards, c.QueueSize, c.MaxPending, c.Attempts}
	spans := []time.Duration{c.RetryBackoff, c.AckTimeout}
	switch {
	case !validToken(c.SubjectRoot):
		return fmt.Errorf("%w: publish subject root %q must be one subject token", apperr.ErrInvalidArgument, c.SubjectRoot)
	case !allPositive(counts) || !allPositive(spans):
		return fmt.Errorf("%w: publish config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Shards > slotmap.Count:
		return fmt.Errorf("%w: publish shards %d exceed %d slots", apperr.ErrInvalidArgument, c.Shards, slotmap.Count)
	default:
		return nil
	}
}

func (c Config) JetStreamOptions(log *slog.Logger) []jetstream.JetStreamOpt {
	c = c.withDefaults()
	return []jetstream.JetStreamOpt{
		jetstream.WithPublishAsyncMaxPending(2 * c.Shards * c.MaxPending),
		jetstream.WithPublishAsyncTimeout(c.AckTimeout),
		jetstream.WithPublishAsyncErrHandler(AsyncFailureHandler(log)),
	}
}

func allPositive[T int | time.Duration](vs []T) bool {
	for _, v := range vs {
		if v <= 0 {
			return false
		}
	}
	return true
}

func validToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ".*> \t\r\n")
}
