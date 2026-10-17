package timer

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	ConsumerName      = "timer-due"
	DefaultMaxAge     = 2 * time.Hour
	DefaultDuplicates = 2 * time.Minute
	DefaultAckWait    = 30 * time.Second
	MaxAckPending     = 1024
)

type StreamConfig struct {
	Name        string
	SubjectRoot string
	Replicas    int
	MaxAge      time.Duration
	Duplicates  time.Duration
	AckWait     time.Duration
}

type Admin interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func ArmSubjects(root string) string { return root + ".arm.>" }

func DueSubject(root string) string { return root + ".due" }

func (c StreamConfig) Validate() error { return c.withDefaults().validate() }

func (c StreamConfig) withDefaults() StreamConfig {
	c.MaxAge = cmp.Or(c.MaxAge, DefaultMaxAge)
	c.Duplicates = cmp.Or(c.Duplicates, DefaultDuplicates)
	c.AckWait = cmp.Or(c.AckWait, DefaultAckWait)
	return c
}

func (c StreamConfig) validate() error {
	switch {
	case !subjectToken(c.Name) || !subjectToken(c.SubjectRoot):
		return fmt.Errorf("%w: timer stream name %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, c.Name, c.SubjectRoot)
	case c.Replicas <= 0 || c.MaxAge <= 0 || c.Duplicates <= 0 || c.AckWait <= 0:
		return fmt.Errorf("%w: timer stream replicas %d, max age %v, duplicate window %v and ack wait %v must be positive", apperr.ErrInvalidArgument, c.Replicas, c.MaxAge, c.Duplicates, c.AckWait)
	case c.Duplicates > c.MaxAge:
		return fmt.Errorf("%w: timer stream duplicate window %v exceeds max age %v", apperr.ErrInvalidArgument, c.Duplicates, c.MaxAge)
	default:
		return nil
	}
}

func EnsureStream(ctx context.Context, js Admin, c StreamConfig) error {
	if js == nil {
		return fmt.Errorf("%w: ensure timer stream needs a jetstream client", apperr.ErrInvalidArgument)
	}
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	if _, err := js.CreateOrUpdateStream(ctx, c.stream()); err != nil {
		return fmt.Errorf("ensure timer stream %s: %w", c.Name, err)
	}
	if _, err := js.CreateOrUpdateConsumer(ctx, c.Name, c.consumer()); err != nil {
		return fmt.Errorf("ensure timer consumer %s on %s: %w", ConsumerName, c.Name, err)
	}
	return nil
}

func (c StreamConfig) stream() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              c.Name,
		Subjects:          []string{ArmSubjects(c.SubjectRoot), DueSubject(c.SubjectRoot)},
		Retention:         jetstream.WorkQueuePolicy,
		Storage:           jetstream.FileStorage,
		Replicas:          c.Replicas,
		MaxAge:            c.MaxAge,
		Duplicates:        c.Duplicates,
		AllowMsgSchedules: true,
	}
}

func (c StreamConfig) consumer() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       ConsumerName,
		FilterSubject: DueSubject(c.SubjectRoot),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       c.AckWait,
		MaxAckPending: MaxAckPending,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}

func subjectToken(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }
