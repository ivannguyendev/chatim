package publish

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultStreamMaxAge     = 7 * 24 * time.Hour
	DefaultStreamDuplicates = 5 * time.Minute

	msgCreated      = "msg_created"
	roomCreated     = "room_created"
	msgEdited       = "msg_edited"
	msgDeleted      = "msg_deleted"
	reactionChanged = "reaction_changed"
	countsChanged   = "counts_changed"
	msgPinned       = "msg_pinned"
	msgUnpinned     = "msg_unpinned"
)

type StreamManager interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

type StreamConfig struct {
	Name        string
	SubjectRoot string
	LiveRoot    string
	Replicas    int
	MaxAge      time.Duration
	Duplicates  time.Duration
}

func (c StreamConfig) Validate() error { return c.withDefaults().validate() }

func (c StreamConfig) withDefaults() StreamConfig {
	c.MaxAge = cmp.Or(c.MaxAge, DefaultStreamMaxAge)
	c.Duplicates = cmp.Or(c.Duplicates, DefaultStreamDuplicates)
	return c
}

func (c StreamConfig) validate() error {
	switch {
	case !validToken(c.Name) || !validToken(c.SubjectRoot) || !validToken(c.LiveRoot):
		return fmt.Errorf("%w: stream name %q, subject root %q and live root %q must be single subject tokens", apperr.ErrInvalidArgument, c.Name, c.SubjectRoot, c.LiveRoot)
	case c.SubjectRoot == c.LiveRoot:
		return fmt.Errorf("%w: subject root and live root must differ, both are %q", apperr.ErrInvalidArgument, c.SubjectRoot)
	case c.Replicas <= 0 || c.MaxAge <= 0 || c.Duplicates <= 0:
		return fmt.Errorf("%w: stream replicas %d, max age %v and duplicate window %v must be positive", apperr.ErrInvalidArgument, c.Replicas, c.MaxAge, c.Duplicates)
	case c.Duplicates > c.MaxAge:
		return fmt.Errorf("%w: stream duplicate window %v exceeds max age %v", apperr.ErrInvalidArgument, c.Duplicates, c.MaxAge)
	default:
		return nil
	}
}

func (c StreamConfig) jetstream() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:       c.Name,
		Subjects:   []string{c.SubjectRoot + ".>"},
		Storage:    jetstream.FileStorage,
		Replicas:   c.Replicas,
		MaxAge:     c.MaxAge,
		Duplicates: c.Duplicates,
		RePublish: &jetstream.RePublish{
			Source:      c.SubjectRoot + ".*.room.*.*",
			Destination: c.LiveRoot + ".{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
		},
	}
}

func EnsureStream(ctx context.Context, js StreamManager, c StreamConfig) error {
	if js == nil {
		return fmt.Errorf("%w: ensure stream needs a jetstream client", apperr.ErrInvalidArgument)
	}
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	if _, err := js.CreateOrUpdateStream(ctx, c.jetstream()); err != nil {
		return fmt.Errorf("ensure stream %s: %w", c.Name, err)
	}
	return nil
}

func roomSubject(root, tenant string, room uint64, kind string) string {
	return root + "." + tenant + ".room." + strconv.FormatUint(room, 10) + "." + kind
}
