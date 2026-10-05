package work

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultPartitions = 32
	DefaultMaxAge     = 2 * time.Hour
	DefaultDuplicates = 2 * time.Minute
	DefaultAckWait    = 35 * time.Second
	MaxAckPending     = 1024
)

type StreamConfig struct {
	Name        string
	SubjectRoot string
	Partitions  int
	Replicas    int
	MaxAge      time.Duration
	Duplicates  time.Duration
	AckWait     time.Duration
}

type Admin interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func (c StreamConfig) Validate() error { return c.withDefaults().validate() }

func (c StreamConfig) withDefaults() StreamConfig {
	c.Partitions = cmp.Or(c.Partitions, DefaultPartitions)
	c.MaxAge = cmp.Or(c.MaxAge, DefaultMaxAge)
	c.Duplicates = cmp.Or(c.Duplicates, DefaultDuplicates)
	c.AckWait = cmp.Or(c.AckWait, DefaultAckWait)
	return c
}

func (c StreamConfig) validate() error {
	switch {
	case !subjectToken(c.Name) || !subjectToken(c.SubjectRoot):
		return fmt.Errorf("%w: work stream name %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, c.Name, c.SubjectRoot)
	case c.Partitions <= 0 || c.Partitions > slotmap.Count:
		return fmt.Errorf("%w: work partitions %d must be between 1 and the slot count %d", apperr.ErrInvalidArgument, c.Partitions, slotmap.Count)
	case c.Replicas <= 0 || c.MaxAge <= 0 || c.Duplicates <= 0 || c.AckWait <= 0:
		return fmt.Errorf("%w: work stream replicas %d, max age %v, duplicate window %v and ack wait %v must be positive", apperr.ErrInvalidArgument, c.Replicas, c.MaxAge, c.Duplicates, c.AckWait)
	case c.Duplicates > c.MaxAge:
		return fmt.Errorf("%w: work stream duplicate window %v exceeds max age %v", apperr.ErrInvalidArgument, c.Duplicates, c.MaxAge)
	default:
		return nil
	}
}

func ConsumerName(partition int) string { return "work-p" + strconv.Itoa(partition) }

func EnsureStream(ctx context.Context, js Admin, c StreamConfig) error {
	if js == nil {
		return fmt.Errorf("%w: ensure work stream needs a jetstream client", apperr.ErrInvalidArgument)
	}
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	if _, err := js.CreateOrUpdateStream(ctx, c.stream()); err != nil {
		return fmt.Errorf("ensure work stream %s: %w", c.Name, err)
	}
	for p := range c.Partitions {
		if _, err := js.CreateOrUpdateConsumer(ctx, c.Name, c.consumer(p)); err != nil {
			return fmt.Errorf("ensure work consumer %s on %s: %w", ConsumerName(p), c.Name, err)
		}
	}
	return nil
}

func (c StreamConfig) stream() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:       c.Name,
		Subjects:   []string{c.SubjectRoot + ".>"},
		Retention:  jetstream.WorkQueuePolicy,
		Storage:    jetstream.FileStorage,
		Replicas:   c.Replicas,
		MaxAge:     c.MaxAge,
		Duplicates: c.Duplicates,
	}
}

func (c StreamConfig) consumer(p int) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       ConsumerName(p),
		FilterSubject: Subject(c.SubjectRoot, p),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       c.AckWait,
		MaxAckPending: MaxAckPending,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}

func subjectToken(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }
