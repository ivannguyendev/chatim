package room

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: room service needs access, rooms, direct rooms, a member service, request dedupe and events", apperr.ErrInvalidArgument)

type Rooms interface {
	InsertRoom(ctx context.Context, r domain.Room) error
	Get(ctx context.Context, id uint64) (domain.Room, error)
	store.RoomUpdater
}

type MemberService interface {
	MemberBatch() int
	ArmCount(ctx context.Context, room uint64) (timer.Timer, error)
	AddFounders(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error)
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	SettleCount(ctx context.Context, room uint64, t timer.Timer, delta int) (domain.MemberCount, bool)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access   *access.Checker
	Rooms    Rooms
	Directs  store.DirectRooms
	Members  MemberService
	Requests member.RequestDedupe
	Events   EventPublisher
	NewID    func() uint64
	Now      func() time.Time
	Log      *slog.Logger
}

type Service struct {
	d Deps
}

func New(d Deps) (*Service, error) {
	if d.Access == nil || d.Rooms == nil || d.Directs == nil || d.Members == nil || d.Requests == nil || d.Events == nil {
		return nil, errMissingDeps
	}
	if d.NewID == nil {
		d.NewID = ids.NewRoomID
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{d: d}, nil
}

func (s *Service) now() time.Time { return s.d.Now().UTC().Truncate(time.Millisecond) }

func (s *Service) tell(room uint64, ev *chatimv1.Event) {
	_ = s.d.Events.Enqueue(room, []*chatimv1.Event{ev})
}
