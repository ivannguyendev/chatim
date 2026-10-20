package grpcsrv

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errMissingDeps = fmt.Errorf("%w: core service needs a sender, a room store, a page reader, a mutator, a member service, a room service, an edit store, a hidden store, a bookmark store, a reply store, a reader interaction store, a room lister, a thread lister, a reactor lister, a pin reader and a member lister", apperr.ErrInvalidArgument)
	errBadRoomID   = fmt.Errorf("%w: room id", apperr.ErrInvalidArgument)
)

type Sender interface {
	Send(ctx context.Context, c actor.SendCmd) (actor.Ack, error)
}

type PageReader interface {
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type Deps struct {
	Sender        Sender
	Rooms         store.Rooms
	Pages         PageReader
	Policy        access.Policy
	Mutator       *mutate.Mutator
	MemberService *member.Service
	RoomService   *room.Service
	Edits         store.Edits
	Hidden        store.Hidden
	Bookmarks     BookmarkLister
	Replies       RepliesLister
	Mine          MyInteractionReader
	RoomList      RoomLister
	Threads       ThreadLister
	Reactors      ReactorLister
	PinBoard      PinReader
	MemberList    MemberLister
}

type Service struct {
	chatimv1.UnimplementedCoreServiceServer
	sender        Sender
	rooms         store.Rooms
	pages         PageReader
	mutator       *mutate.Mutator
	memberService *member.Service
	roomService   *room.Service
	edits         store.Edits
	hidden        store.Hidden
	bookmarks     BookmarkLister
	replies       RepliesLister
	mine          MyInteractionReader
	roomList      RoomLister
	threads       ThreadLister
	reactors      ReactorLister
	pinBoard      PinReader
	memberList    MemberLister
	access        *access.Checker
	view          view.Pipeline
	maskView      view.Pipeline
	log           *slog.Logger
}

var _ chatimv1.CoreServiceServer = (*Service)(nil)

func New(d Deps, log *slog.Logger) (*Service, error) {
	if d.Sender == nil || d.Rooms == nil || d.Pages == nil || d.Mutator == nil || d.MemberService == nil || d.RoomService == nil || d.Edits == nil || d.Hidden == nil || d.Bookmarks == nil ||
		d.Replies == nil || d.Mine == nil || d.RoomList == nil ||
		d.Threads == nil || d.Reactors == nil || d.PinBoard == nil || d.MemberList == nil {
		return nil, errMissingDeps
	}
	if log == nil {
		log = slog.Default()
	}
	checker, err := access.NewChecker(d.Rooms, d.Policy)
	if err != nil {
		return nil, err
	}
	return &Service{
		sender: d.Sender, rooms: d.Rooms, pages: d.Pages, mutator: d.Mutator, memberService: d.MemberService, roomService: d.RoomService, edits: d.Edits, hidden: d.Hidden,
		bookmarks: d.Bookmarks, replies: d.Replies, mine: d.Mine, roomList: d.RoomList, threads: d.Threads, reactors: d.Reactors, pinBoard: d.PinBoard, memberList: d.MemberList, access: checker, view: view.Default(), maskView: view.New(view.MaskDeleted, view.HideForViewer), log: log,
	}, nil
}

func (s *Service) SendMessage(ctx context.Context, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, error) {
	who, err := callerid.Of(ctx)
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
