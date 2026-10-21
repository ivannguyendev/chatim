package command

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	requestOneof = (&chatimv1.ClientRequest{}).ProtoReflect().Descriptor().Oneofs().ByName("request")
	resultOneof  = (&chatimv1.ServerResponse{}).ProtoReflect().Descriptor().Oneofs().ByName("result")

	errNoClient     = status.Error(codes.Unimplemented, "service not served by this core connection")
	errWrongRequest = status.Error(codes.Internal, "request type does not match its command")
)

type caller func(ctx context.Context, conn coreroute.Conn, req proto.Message) (proto.Message, error)

type spec struct {
	request  protoreflect.MessageDescriptor
	response protoreflect.MessageDescriptor
	call     caller
}

type command struct {
	index  int
	name   string
	result protoreflect.FieldDescriptor
	room   protoreflect.FieldDescriptor
	check  func(proto.Message) error
	call   caller
}

func via[C any, Req, Resp proto.Message](client func(coreroute.Conn) (C, bool), method func(C, context.Context, Req, ...grpc.CallOption) (Resp, error)) spec {
	var req Req
	var resp Resp
	return spec{
		request:  req.ProtoReflect().Descriptor(),
		response: resp.ProtoReflect().Descriptor(),
		call: func(ctx context.Context, conn coreroute.Conn, m proto.Message) (proto.Message, error) {
			c, ok := client(conn)
			if !ok {
				return nil, errNoClient
			}
			typed, ok := m.(Req)
			if !ok {
				return nil, errWrongRequest
			}
			return method(c, ctx, typed)
		},
	}
}

func coreClient(c coreroute.Conn) (chatimv1.CoreServiceClient, bool) { return c.Core, c.Core != nil }

func mentionClient(c coreroute.Conn) (chatimv1.MentionServiceClient, bool) {
	return c.Mention, c.Mention != nil
}

func specs() []spec {
	return []spec{
		via(coreClient, chatimv1.CoreServiceClient.CreateRoom),
		via(coreClient, chatimv1.CoreServiceClient.OpenDirectRoom),
		via(coreClient, chatimv1.CoreServiceClient.ListMyRooms),
		via(coreClient, chatimv1.CoreServiceClient.GetRooms),
		via(coreClient, chatimv1.CoreServiceClient.UpdateRoom),
		via(coreClient, chatimv1.CoreServiceClient.SetRoomPreferences),
		via(coreClient, chatimv1.CoreServiceClient.SendMessage),
		via(coreClient, chatimv1.CoreServiceClient.GetHistory),
		via(coreClient, chatimv1.CoreServiceClient.GetMessages),
		via(coreClient, chatimv1.CoreServiceClient.EditMessage),
		via(coreClient, chatimv1.CoreServiceClient.DeleteMessage),
		via(coreClient, chatimv1.CoreServiceClient.HideMessage),
		via(coreClient, chatimv1.CoreServiceClient.ClearHistory),
		via(coreClient, chatimv1.CoreServiceClient.GetEditHistory),
		via(coreClient, chatimv1.CoreServiceClient.ReactMessage),
		via(coreClient, chatimv1.CoreServiceClient.PinMessage),
		via(coreClient, chatimv1.CoreServiceClient.UnpinMessage),
		via(coreClient, chatimv1.CoreServiceClient.ListPins),
		via(coreClient, chatimv1.CoreServiceClient.GetReactionSettings),
		via(coreClient, chatimv1.CoreServiceClient.GetReactions),
		via(coreClient, chatimv1.CoreServiceClient.SetBookmark),
		via(coreClient, chatimv1.CoreServiceClient.GetReplies),
		via(coreClient, chatimv1.CoreServiceClient.ListThreads),
		via(coreClient, chatimv1.CoreServiceClient.ListBookmarks),
		via(coreClient, chatimv1.CoreServiceClient.AddMembers),
		via(coreClient, chatimv1.CoreServiceClient.RemoveMember),
		via(coreClient, chatimv1.CoreServiceClient.LeaveRoom),
		via(coreClient, chatimv1.CoreServiceClient.ChangeMemberRole),
		via(coreClient, chatimv1.CoreServiceClient.SetMemberPriority),
		via(coreClient, chatimv1.CoreServiceClient.ListMembers),
		via(coreClient, chatimv1.CoreServiceClient.MarkUnread),
		via(mentionClient, chatimv1.MentionServiceClient.ListMentions),
		via(mentionClient, chatimv1.MentionServiceClient.ListRoomGroups),
	}
}

func checks() map[protoreflect.FullName]func(proto.Message) error {
	return map[protoreflect.FullName]func(proto.Message) error{
		(&chatimv1.SendMessageRequest{}).ProtoReflect().Descriptor().FullName(): checkSendMessage,
	}
}

func buildCommands() ([]*command, error) {
	byRequest := map[protoreflect.FullName]spec{}
	for _, s := range specs() {
		if _, dup := byRequest[s.request.FullName()]; dup {
			return nil, fmt.Errorf("command: %s listed twice", s.request.FullName())
		}
		byRequest[s.request.FullName()] = s
	}
	fields, checkFor := requestOneof.Fields(), checks()
	cmds := make([]*command, fields.Len())
	for i := range fields.Len() {
		cmd, err := buildCommand(i, fields.Get(i), byRequest)
		if err == nil {
			cmd.check = checkFor[fields.Get(i).Message().FullName()]
		}
		if err != nil {
			return nil, err
		}
		cmds[i] = cmd
	}
	if len(byRequest) > 0 {
		return nil, fmt.Errorf("command: %d core calls have no client request field", len(byRequest))
	}
	return cmds, nil
}

func buildCommand(index int, field protoreflect.FieldDescriptor, byRequest map[protoreflect.FullName]spec) (*command, error) {
	request := field.Message()
	s, ok := byRequest[request.FullName()]
	if !ok {
		return nil, fmt.Errorf("command: %s has no core call", request.FullName())
	}
	delete(byRequest, request.FullName())
	result := resultOneof.Fields().ByNumber(field.Number())
	if result == nil || result.Message() == nil || result.Message().FullName() != s.response.FullName() {
		return nil, fmt.Errorf("command: result field %d does not carry %s", field.Number(), s.response.FullName())
	}
	room := request.Fields().ByName("room_id")
	if room != nil && (room.Kind() != protoreflect.StringKind || room.IsList()) {
		return nil, fmt.Errorf("command: %s room_id is not a single string", request.FullName())
	}
	return &command{
		index:  index,
		name:   strings.TrimSuffix(string(request.Name()), "Request"),
		result: result,
		room:   room,
		call:   s.call,
	}, nil
}
