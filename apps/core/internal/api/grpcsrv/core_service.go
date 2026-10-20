package grpcsrv

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
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
	errMissingDeps = fmt.Errorf("%w: core service needs a sender, a room store, a page reader, a message editor, a message reader, an interaction service, a member service, a room service, a bookmark store, a reply store, a room lister, a thread lister, a reactor lister, a pin reader and a member lister", apperr.ErrInvalidArgument)
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
	Sender             Sender
	Rooms              store.Rooms
	Pages              PageReader
	Policy             access.Policy
	Editor             *message.Editor
	Reader             *message.Reader
	InteractionService *interaction.Service
	MemberService      *member.Service
	RoomService        *room.Service
	Bookmarks          BookmarkLister
	Replies            RepliesLister
	RoomList           RoomLister
	Threads            ThreadLister
	Reactors           ReactorLister
	PinBoard           PinReader
	MemberList         MemberLister
}

type Service struct {
	chatimv1.UnimplementedCoreServiceServer
	sender             Sender
	rooms              store.Rooms
	pages              PageReader
	editor             *message.Editor
	reader             *message.Reader
	interactionService *interaction.Service
	memberService      *member.Service
	roomService        *room.Service
	bookmarks          BookmarkLister
	replies            RepliesLister
	roomList           RoomLister
	threads            ThreadLister
	reactors           ReactorLister
	pinBoard           PinReader
	memberList         MemberLister
	access             *access.Checker
}

var _ chatimv1.CoreServiceServer = (*Service)(nil)

func New(d Deps) (*Service, error) {
	if d.Sender == nil || d.Rooms == nil || d.Pages == nil || d.Editor == nil || d.Reader == nil || d.InteractionService == nil || d.MemberService == nil || d.RoomService == nil || d.Bookmarks == nil ||
		d.Replies == nil || d.RoomList == nil ||
		d.Threads == nil || d.Reactors == nil || d.PinBoard == nil || d.MemberList == nil {
		return nil, errMissingDeps
	}
	checker, err := access.NewChecker(d.Rooms, d.Policy)
	if err != nil {
		return nil, err
	}
	return &Service{
		sender: d.Sender, rooms: d.Rooms, pages: d.Pages, editor: d.Editor, reader: d.Reader, interactionService: d.InteractionService, memberService: d.MemberService, roomService: d.RoomService,
		bookmarks: d.Bookmarks, replies: d.Replies, roomList: d.RoomList, threads: d.Threads, reactors: d.Reactors, pinBoard: d.PinBoard, memberList: d.MemberList, access: checker,
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
