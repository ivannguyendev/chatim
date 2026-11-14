package chatrpc

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
	errNoClient     = status.Error(codes.Unimplemented, "service not served by this chat node connection")
	errWrongRequest = status.Error(codes.Internal, "request type does not match its command")
)

type Clients struct {
	Chat    chatimv1.ChatServiceClient
	Mention chatimv1.MentionServiceClient
	Media   chatimv1.MediaServiceClient
}

type caller func(ctx context.Context, conn Clients, req proto.Message) (proto.Message, error)

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

func via[C any, Req, Resp proto.Message](method string, client func(Clients) (C, bool), call func(C, context.Context, Req, ...grpc.CallOption) (Resp, error)) spec {
	var req Req
	var resp Resp
	return spec{
		method:   method,
		request:  req.ProtoReflect().Descriptor(),
		response: resp.ProtoReflect().Descriptor(),
		call: func(ctx context.Context, conn Clients, m proto.Message) (proto.Message, error) {
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

func chatClient(c Clients) (chatimv1.ChatServiceClient, bool) { return c.Chat, c.Chat != nil }

func mentionClient(c Clients) (chatimv1.MentionServiceClient, bool) {
	return c.Mention, c.Mention != nil
}

func mediaClient(c Clients) (chatimv1.MediaServiceClient, bool) { return c.Media, c.Media != nil }

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
			return nil, fmt.Errorf("chatrpc: %s is not a client chat call", m)
		case seen[m]:
			return nil, fmt.Errorf("chatrpc: %s listed twice", m)
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
			return nil, fmt.Errorf("chatrpc: %s listed twice", s.method)
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
		return Command{}, fmt.Errorf("chatrpc: %s is not a full method name", s.method)
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return Command{}, fmt.Errorf("chatrpc: service of %s: %w", s.method, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return Command{}, fmt.Errorf("chatrpc: %s is not a service", service)
	}
	md := sd.Methods().ByName(protoreflect.Name(name))
	if md == nil || md.Input().FullName() != s.request.FullName() || md.Output().FullName() != s.response.FullName() {
		return Command{}, fmt.Errorf("chatrpc: %s does not take %s and return %s", s.method, s.request.FullName(), s.response.FullName())
	}
	room := s.request.Fields().ByName("room_id")
	if room != nil && (room.Kind() != protoreflect.StringKind || room.IsList()) {
		return Command{}, fmt.Errorf("chatrpc: %s room_id is not a single string", s.request.FullName())
	}
	return Command{Method: s.method, Name: name, Request: s.request, Response: s.response, Room: room, call: s.call}, nil
}

func tableSpecs() []spec {
	return []spec{
		via(chatimv1.ChatService_CreateRoom_FullMethodName, chatClient, chatimv1.ChatServiceClient.CreateRoom),
		via(chatimv1.ChatService_OpenDirectRoom_FullMethodName, chatClient, chatimv1.ChatServiceClient.OpenDirectRoom),
		via(chatimv1.ChatService_ListMyRooms_FullMethodName, chatClient, chatimv1.ChatServiceClient.ListMyRooms),
		via(chatimv1.ChatService_GetRooms_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetRooms),
		via(chatimv1.ChatService_UpdateRoom_FullMethodName, chatClient, chatimv1.ChatServiceClient.UpdateRoom),
		via(chatimv1.ChatService_SetRoomPreferences_FullMethodName, chatClient, chatimv1.ChatServiceClient.SetRoomPreferences),
		via(chatimv1.ChatService_SendMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.SendMessage),
		via(chatimv1.ChatService_GetHistory_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetHistory),
		via(chatimv1.ChatService_GetMessages_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetMessages),
		via(chatimv1.ChatService_EditMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.EditMessage),
		via(chatimv1.ChatService_DeleteMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.DeleteMessage),
		via(chatimv1.ChatService_HideMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.HideMessage),
		via(chatimv1.ChatService_ClearHistory_FullMethodName, chatClient, chatimv1.ChatServiceClient.ClearHistory),
		via(chatimv1.ChatService_GetEditHistory_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetEditHistory),
		via(chatimv1.ChatService_ReactMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.ReactMessage),
		via(chatimv1.ChatService_PinMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.PinMessage),
		via(chatimv1.ChatService_UnpinMessage_FullMethodName, chatClient, chatimv1.ChatServiceClient.UnpinMessage),
		via(chatimv1.ChatService_ListPins_FullMethodName, chatClient, chatimv1.ChatServiceClient.ListPins),
		via(chatimv1.ChatService_GetReactionSettings_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetReactionSettings),
		via(chatimv1.ChatService_GetReactions_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetReactions),
		via(chatimv1.ChatService_SetBookmark_FullMethodName, chatClient, chatimv1.ChatServiceClient.SetBookmark),
		via(chatimv1.ChatService_GetReplies_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetReplies),
		via(chatimv1.ChatService_ListThreads_FullMethodName, chatClient, chatimv1.ChatServiceClient.ListThreads),
		via(chatimv1.ChatService_ListBookmarks_FullMethodName, chatClient, chatimv1.ChatServiceClient.ListBookmarks),
		via(chatimv1.ChatService_AddMembers_FullMethodName, chatClient, chatimv1.ChatServiceClient.AddMembers),
		via(chatimv1.ChatService_RemoveMember_FullMethodName, chatClient, chatimv1.ChatServiceClient.RemoveMember),
		via(chatimv1.ChatService_LeaveRoom_FullMethodName, chatClient, chatimv1.ChatServiceClient.LeaveRoom),
		via(chatimv1.ChatService_ChangeMemberRole_FullMethodName, chatClient, chatimv1.ChatServiceClient.ChangeMemberRole),
		via(chatimv1.ChatService_SetMemberPriority_FullMethodName, chatClient, chatimv1.ChatServiceClient.SetMemberPriority),
		via(chatimv1.ChatService_ListMembers_FullMethodName, chatClient, chatimv1.ChatServiceClient.ListMembers),
		via(chatimv1.ChatService_MarkUnread_FullMethodName, chatClient, chatimv1.ChatServiceClient.MarkUnread),
		via(chatimv1.ChatService_MarkRead_FullMethodName, chatClient, chatimv1.ChatServiceClient.MarkRead),
		via(chatimv1.MentionService_ListMentions_FullMethodName, mentionClient, chatimv1.MentionServiceClient.ListMentions),
		via(chatimv1.MentionService_ListRoomGroups_FullMethodName, mentionClient, chatimv1.MentionServiceClient.ListRoomGroups),
		via(chatimv1.ChatService_GetClientConfig_FullMethodName, chatClient, chatimv1.ChatServiceClient.GetClientConfig),
		via(chatimv1.MediaService_StartUpload_FullMethodName, mediaClient, chatimv1.MediaServiceClient.StartUpload),
		via(chatimv1.MediaService_GetPartURLs_FullMethodName, mediaClient, chatimv1.MediaServiceClient.GetPartURLs),
		via(chatimv1.MediaService_CompleteUpload_FullMethodName, mediaClient, chatimv1.MediaServiceClient.CompleteUpload),
		via(chatimv1.MediaService_AbortUpload_FullMethodName, mediaClient, chatimv1.MediaServiceClient.AbortUpload),
		via(chatimv1.MediaService_GetAttachmentURLs_FullMethodName, mediaClient, chatimv1.MediaServiceClient.GetAttachmentURLs),
		via(chatimv1.MediaService_ListUploads_FullMethodName, mediaClient, chatimv1.MediaServiceClient.ListUploads),
		via(chatimv1.MediaService_ListRoomFiles_FullMethodName, mediaClient, chatimv1.MediaServiceClient.ListRoomFiles),
		via(chatimv1.MediaService_GetPublicURLs_FullMethodName, mediaClient, chatimv1.MediaServiceClient.GetPublicURLs),
	}
}
