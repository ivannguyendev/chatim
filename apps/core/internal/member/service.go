package member

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: member service needs access, messages, rooms, events, members, request dedupe, a member forgetter, member count timers and read positions", apperr.ErrInvalidArgument)

type Messages interface {
	Last(ctx context.Context, room, thread uint64) (uint64, error)
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, bool, error)
}

type RoomWriter interface {
	HistoryClearer
	store.RoomPreferences
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access       *access.Checker
	Messages     Messages
	Rooms        RoomWriter
	Events       EventPublisher
	Members      MemberStore
	Requests     RequestDedupe
	Forget       MemberForgetter
	Timers       CountTimers
	Reads        store.ReadPositions
	Limits       Limits
	Now          func() time.Time
	Log          *slog.Logger
	NewRequestID func() string
}

type Service struct {
	d Deps
}

func New(d Deps) (*Service, error) {
	if d.Access == nil || d.Messages == nil || d.Rooms == nil || d.Events == nil || d.Members == nil ||
		d.Requests == nil || d.Forget == nil || d.Timers == nil || d.Reads == nil {
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
	if d.NewRequestID == nil {
		d.NewRequestID = randomRequestID
	}
	return &Service{d: d}, nil
}

func (s *Service) MemberBatch() int { return s.d.Limits.MemberBatch }

func (s *Service) now() time.Time { return s.d.Now().UTC().Truncate(time.Millisecond) }

func (s *Service) tell(room uint64, ev *chatimv1.Event) {
	_ = s.d.Events.Enqueue(room, []*chatimv1.Event{ev})
}
