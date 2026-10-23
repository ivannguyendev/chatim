package corecall

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errNoClient     = status.Error(codes.Unimplemented, "service not served by this core connection")
	errWrongRequest = status.Error(codes.Internal, "request type does not match its command")
)

type Conn struct {
	Core    chatimv1.CoreServiceClient
	Mention chatimv1.MentionServiceClient
}

type caller func(ctx context.Context, conn Conn, req proto.Message) (proto.Message, error)

type Command struct {
	Method   string
	Name     string
	Request  protoreflect.MessageDescriptor
	Response protoreflect.MessageDescriptor
	Room     protoreflect.FieldDescriptor
	call     caller
}

func (c Command) RoomID(req proto.Message) (string, bool) {
	if c.Room == nil {
		return "", false
	}
	return req.ProtoReflect().Get(c.Room).String(), true
}

type spec struct {
	method   string
	request  protoreflect.MessageDescriptor
	response protoreflect.MessageDescriptor
	call     caller
}

func via[C any, Req, Resp proto.Message](method string, client func(Conn) (C, bool), call func(C, context.Context, Req, ...grpc.CallOption) (Resp, error)) spec {
	var req Req
	var resp Resp
	return spec{
		method:   method,
		request:  req.ProtoReflect().Descriptor(),
		response: resp.ProtoReflect().Descriptor(),
		call: func(ctx context.Context, conn Conn, m proto.Message) (proto.Message, error) {
			c, ok := client(conn)
			if !ok {
				return nil, errNoClient
			}
			typed, ok := m.(Req)
			if !ok {
				return nil, errWrongRequest
			}
			return call(c, ctx, typed)
		},
	}
}

func coreClient(c Conn) (chatimv1.CoreServiceClient, bool) { return c.Core, c.Core != nil }

func mentionClient(c Conn) (chatimv1.MentionServiceClient, bool) {
	return c.Mention, c.Mention != nil
}

var table = sync.OnceValues(buildTable)

func Commands(methods []string) ([]Command, error) {
	byMethod, err := table()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(methods))
	out := make([]Command, 0, len(methods))
	for _, m := range methods {
		cmd, ok := byMethod[m]
		switch {
		case !ok:
			return nil, fmt.Errorf("corecall: %s is not a client core call", m)
		case seen[m]:
			return nil, fmt.Errorf("corecall: %s listed twice", m)
		}
		seen[m] = true
		out = append(out, cmd)
	}
	return out, nil
}

func buildTable() (map[string]Command, error) {
	specs := tableSpecs()
	byMethod := make(map[string]Command, len(specs))
	for _, s := range specs {
		if _, dup := byMethod[s.method]; dup {
			return nil, fmt.Errorf("corecall: %s listed twice", s.method)
		}
		cmd, err := buildCommand(s)
		if err != nil {
			return nil, err
		}
		byMethod[s.method] = cmd
	}
	return byMethod, nil
}

func buildCommand(s spec) (Command, error) {
	service, name, ok := strings.Cut(strings.TrimPrefix(s.method, "/"), "/")
	if !ok {
		return Command{}, fmt.Errorf("corecall: %s is not a full method name", s.method)
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return Command{}, fmt.Errorf("corecall: service of %s: %w", s.method, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return Command{}, fmt.Errorf("corecall: %s is not a service", service)
	}
	md := sd.Methods().ByName(protoreflect.Name(name))
	if md == nil || md.Input().FullName() != s.request.FullName() || md.Output().FullName() != s.response.FullName() {
		return Command{}, fmt.Errorf("corecall: %s does not take %s and return %s", s.method, s.request.FullName(), s.response.FullName())
	}
	room := s.request.Fields().ByName("room_id")
	if room != nil && (room.Kind() != protoreflect.StringKind || room.IsList()) {
		return Command{}, fmt.Errorf("corecall: %s room_id is not a single string", s.request.FullName())
	}
	return Command{Method: s.method, Name: name, Request: s.request, Response: s.response, Room: room, call: s.call}, nil
}

func tableSpecs() []spec {
	return []spec{
		via(chatimv1.CoreService_CreateRoom_FullMethodName, coreClient, chatimv1.CoreServiceClient.CreateRoom),
		via(chatimv1.CoreService_OpenDirectRoom_FullMethodName, coreClient, chatimv1.CoreServiceClient.OpenDirectRoom),
		via(chatimv1.CoreService_ListMyRooms_FullMethodName, coreClient, chatimv1.CoreServiceClient.ListMyRooms),
		via(chatimv1.CoreService_GetRooms_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetRooms),
		via(chatimv1.CoreService_UpdateRoom_FullMethodName, coreClient, chatimv1.CoreServiceClient.UpdateRoom),
		via(chatimv1.CoreService_SetRoomPreferences_FullMethodName, coreClient, chatimv1.CoreServiceClient.SetRoomPreferences),
		via(chatimv1.CoreService_SendMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.SendMessage),
		via(chatimv1.CoreService_GetHistory_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetHistory),
		via(chatimv1.CoreService_GetMessages_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetMessages),
		via(chatimv1.CoreService_EditMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.EditMessage),
		via(chatimv1.CoreService_DeleteMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.DeleteMessage),
		via(chatimv1.CoreService_HideMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.HideMessage),
		via(chatimv1.CoreService_ClearHistory_FullMethodName, coreClient, chatimv1.CoreServiceClient.ClearHistory),
		via(chatimv1.CoreService_GetEditHistory_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetEditHistory),
		via(chatimv1.CoreService_ReactMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.ReactMessage),
		via(chatimv1.CoreService_PinMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.PinMessage),
		via(chatimv1.CoreService_UnpinMessage_FullMethodName, coreClient, chatimv1.CoreServiceClient.UnpinMessage),
		via(chatimv1.CoreService_ListPins_FullMethodName, coreClient, chatimv1.CoreServiceClient.ListPins),
		via(chatimv1.CoreService_GetReactionSettings_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetReactionSettings),
		via(chatimv1.CoreService_GetReactions_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetReactions),
		via(chatimv1.CoreService_SetBookmark_FullMethodName, coreClient, chatimv1.CoreServiceClient.SetBookmark),
		via(chatimv1.CoreService_GetReplies_FullMethodName, coreClient, chatimv1.CoreServiceClient.GetReplies),
		via(chatimv1.CoreService_ListThreads_FullMethodName, coreClient, chatimv1.CoreServiceClient.ListThreads),
		via(chatimv1.CoreService_ListBookmarks_FullMethodName, coreClient, chatimv1.CoreServiceClient.ListBookmarks),
		via(chatimv1.CoreService_AddMembers_FullMethodName, coreClient, chatimv1.CoreServiceClient.AddMembers),
		via(chatimv1.CoreService_RemoveMember_FullMethodName, coreClient, chatimv1.CoreServiceClient.RemoveMember),
		via(chatimv1.CoreService_LeaveRoom_FullMethodName, coreClient, chatimv1.CoreServiceClient.LeaveRoom),
		via(chatimv1.CoreService_ChangeMemberRole_FullMethodName, coreClient, chatimv1.CoreServiceClient.ChangeMemberRole),
		via(chatimv1.CoreService_SetMemberPriority_FullMethodName, coreClient, chatimv1.CoreServiceClient.SetMemberPriority),
		via(chatimv1.CoreService_ListMembers_FullMethodName, coreClient, chatimv1.CoreServiceClient.ListMembers),
		via(chatimv1.CoreService_MarkUnread_FullMethodName, coreClient, chatimv1.CoreServiceClient.MarkUnread),
		via(chatimv1.CoreService_MarkRead_FullMethodName, coreClient, chatimv1.CoreServiceClient.MarkRead),
		via(chatimv1.MentionService_ListMentions_FullMethodName, mentionClient, chatimv1.MentionServiceClient.ListMentions),
		via(chatimv1.MentionService_ListRoomGroups_FullMethodName, mentionClient, chatimv1.MentionServiceClient.ListRoomGroups),
	}
}
