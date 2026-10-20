package grpcsrv

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errMissingDeps = fmt.Errorf("%w: core service needs a sender, a message editor, a message reader, an interaction service, an interaction reader, a member service, a member reader, a room service and a room lister", apperr.ErrInvalidArgument)
	errBadRoomID   = fmt.Errorf("%w: room id", apperr.ErrInvalidArgument)
)

type Sender interface {
	Send(ctx context.Context, c actor.SendCmd) (actor.Ack, error)
}

type Deps struct {
	Sender             Sender
	Editor             *message.Editor
	Reader             *message.Reader
	InteractionService *interaction.Service
	InteractionReader  *interaction.Reader
	MemberService      *member.Service
	MemberReader       *member.Reader
	RoomService        *room.Service
	RoomList           RoomLister
}

type Service struct {
	chatimv1.UnimplementedCoreServiceServer
	sender             Sender
	editor             *message.Editor
	reader             *message.Reader
	interactionService *interaction.Service
	interactionReader  *interaction.Reader
	memberService      *member.Service
	memberReader       *member.Reader
	roomService        *room.Service
	roomList           RoomLister
}

var _ chatimv1.CoreServiceServer = (*Service)(nil)

func New(d Deps) (*Service, error) {
	if d.Sender == nil || d.Editor == nil || d.Reader == nil || d.InteractionService == nil || d.InteractionReader == nil ||
		d.MemberService == nil || d.MemberReader == nil || d.RoomService == nil || d.RoomList == nil {
		return nil, errMissingDeps
	}
	return &Service{
		sender: d.Sender, editor: d.Editor, reader: d.Reader, interactionService: d.InteractionService, interactionReader: d.InteractionReader,
		memberService: d.MemberService, memberReader: d.MemberReader, roomService: d.RoomService, roomList: d.RoomList,
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
