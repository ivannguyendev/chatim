package grpcsrv

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errMissingDeps = fmt.Errorf("%w: core service needs a sender, a room store, a page reader, a mutator, an edit store and a hidden store", apperr.ErrInvalidArgument)
	errBadRoomID   = fmt.Errorf("%w: room id", apperr.ErrInvalidArgument)
)

type Sender interface {
	Send(ctx context.Context, c actor.SendCmd) (actor.Ack, error)
}

type PageReader interface {
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type noEvents struct{}

func (noEvents) Enqueue(uint64, []*chatimv1.Event) error { return nil }

type Deps struct {
	Sender  Sender
	Rooms   store.Rooms
	Pages   PageReader
	Policy  access.Policy
	Events  EventPublisher
	Mutator *mutate.Mutator
	Edits   store.Edits
	Hidden  store.Hidden
	NewID   func() uint64
	Now     func() time.Time
}

type Service struct {
	chatimv1.UnimplementedCoreServiceServer
	sender  Sender
	rooms   store.Rooms
	pages   PageReader
	events  EventPublisher
	mutator *mutate.Mutator
	edits   store.Edits
	hidden  store.Hidden
	access  *access.Checker
	view    view.Pipeline
	newID   func() uint64
	now     func() time.Time
	log     *slog.Logger
}

var _ chatimv1.CoreServiceServer = (*Service)(nil)

func New(d Deps, log *slog.Logger) (*Service, error) {
	if d.Sender == nil || d.Rooms == nil || d.Pages == nil || d.Mutator == nil || d.Edits == nil || d.Hidden == nil {
		return nil, errMissingDeps
	}
	if d.NewID == nil {
		d.NewID = ids.NewRoomID
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Events == nil {
		d.Events = noEvents{}
	}
	if log == nil {
		log = slog.Default()
	}
	checker, err := access.NewChecker(d.Rooms, d.Policy)
	if err != nil {
		return nil, err
	}
	return &Service{
		sender: d.Sender, rooms: d.Rooms, pages: d.Pages, events: d.Events, mutator: d.Mutator, edits: d.Edits, hidden: d.Hidden,
		access: checker, view: view.Default(), newID: d.NewID, now: d.Now, log: log,
	}, nil
}

func (s *Service) SendMessage(ctx context.Context, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	room, err := parseRoomID(req.GetRoomId())
	if err != nil {
		return nil, err
	}
	cmd, err := s.sendCmd(ctx, who, room, req)
	if err != nil {
		return nil, err
	}
	ack, err := s.sender.Send(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.SendMessageResponse{Seq: ack.Seq, CreatedAt: timestamppb.New(ack.CreatedAt)}, nil
}

func parseRoomID(s string) (uint64, error) {
	id, err := ids.ParseRoomID(s)
	if err != nil {
		return 0, errBadRoomID
	}
	return id, nil
}
