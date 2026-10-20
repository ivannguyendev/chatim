package interaction

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: interaction service needs access, messages, interactions, reaction counts, message count timers, pins, a pin projector and events", apperr.ErrInvalidArgument)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access       *access.Checker
	Messages     Messages
	Interactions store.Interactions
	Counts       ReactionCounts
	CountTimers  MessageCountTimers
	Pins         store.Pins
	Projector    PinProjector
	Events       EventPublisher
	Limits       Limits
	Now          func() time.Time
	Log          *slog.Logger
}

type Service struct {
	d Deps
}

func New(d Deps) (*Service, error) {
	if d.Access == nil || d.Messages == nil || d.Interactions == nil || d.Counts == nil || d.CountTimers == nil ||
		d.Pins == nil || d.Projector == nil || d.Events == nil {
		return nil, errMissingDeps
	}
	d.Limits = d.Limits.withDefaults()
	if err := d.Limits.validate(); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{d: d}, nil
}

const settleTimeout = 2 * time.Second

func settling(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
}

func (s *Service) now() time.Time { return s.d.Now().UTC().Truncate(time.Millisecond) }

func (s *Service) tell(room uint64, ev *chatimv1.Event) {
	_ = s.d.Events.Enqueue(room, []*chatimv1.Event{ev})
}

func validKey(key store.MsgKey) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidateThread(key.Thread)
}
