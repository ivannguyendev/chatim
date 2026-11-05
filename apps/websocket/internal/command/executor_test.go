package command_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var testClaims = auth.Claims{Tenant: "t1", User: "u1"}

var realtimeCommands = map[string]bool{
	"SendMessage": true, "EditMessage": true, "DeleteMessage": true, "HideMessage": true, "ReactMessage": true, "PinMessage": true,
	"UnpinMessage": true, "SetBookmark": true, "MarkRead": true, "MarkUnread": true, "ClearHistory": true, "SetRoomPreferences": true,
}

type harness struct {
	exec   *command.Executor
	chat   *fakeChat
	router *fakeRouter
}

func newHarness(t *testing.T, deadline time.Duration) harness {
	t.Helper()
	chat, conn := startFakeChatNode(t)
	router := &fakeRouter{conn: conn}
	exec, err := command.New(router, deadline)
	if err != nil {
		t.Fatal(err)
	}
	return harness{exec: exec, chat: chat, router: router}
}

func requestFields() []protoreflect.FieldDescriptor {
	oneof := (&chatimv1.ClientRequest{}).ProtoReflect().Descriptor().Oneofs().ByName("request")
	fields := make([]protoreflect.FieldDescriptor, oneof.Fields().Len())
	for i := range fields {
		fields[i] = oneof.Fields().Get(i)
	}
	return fields
}

func requestFor(field protoreflect.FieldDescriptor) *chatimv1.ClientRequest {
	req := &chatimv1.ClientRequest{Id: 9}
	msg := req.ProtoReflect()
	inner := msg.NewField(field).Message()
	if room := field.Message().Fields().ByName("room_id"); room != nil {
		inner.Set(room, protoreflect.ValueOfString("42"))
	}
	msg.Set(field, protoreflect.ValueOfMessage(inner))
	return req
}

func commandName(field protoreflect.FieldDescriptor) string {
	return strings.TrimSuffix(string(field.Message().Name()), "Request")
}

func sendRequest(send *chatimv1.SendMessageRequest) *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Id: 5, Request: &chatimv1.ClientRequest_SendMessage{SendMessage: send}}
}

func TestEveryRealtimeCommandGoesToItsChatOnce(t *testing.T) {
	fields := requestFields()
	if len(fields) != 34 {
		t.Fatalf("client request fields = %d, want 34", len(fields))
	}
	served := 0
	for _, field := range fields {
		name := commandName(field)
		if !realtimeCommands[name] {
			continue
		}
		served++
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, time.Second)
			resp, outcome := h.exec.Execute(t.Context(), testClaims, requestFor(field))
			if resp.GetCode() != 0 || outcome.Code != codes.OK || outcome.Command != name || resp.GetId() != 9 {
				t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
			}
			result := resp.ProtoReflect().WhichOneof(resp.ProtoReflect().Descriptor().Oneofs().ByName("result"))
			if result == nil || result.Number() != field.Number() || string(result.Message().Name()) != name+"Response" {
				t.Fatalf("result field = %v", result)
			}
			if len(h.router.routes) != 1 || h.router.routes[0] != "room:42" {
				t.Fatalf("routes = %v, want room:42", h.router.routes)
			}
			calls := h.chat.recorded()
			if len(calls) != 1 || calls[0].method != "/chatim.v1.ChatService/"+name {
				t.Fatalf("chat calls = %+v", calls)
			}
		})
	}
	if served != 12 {
		t.Fatalf("realtime commands = %d, want 12", served)
	}
}

func TestRequestCommandsTakeTheUnknownRequestPath(t *testing.T) {
	refused := 0
	for _, field := range requestFields() {
		name := commandName(field)
		if realtimeCommands[name] {
			continue
		}
		refused++
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, time.Second)
			resp, outcome := h.exec.Execute(t.Context(), testClaims, requestFor(field))
			if codes.Code(resp.GetCode()) != codes.Unimplemented || resp.GetMessage() != "request not supported" || resp.GetId() != 9 || resp.GetResult() != nil {
				t.Fatalf("resp = %v", resp)
			}
			if outcome.Code != codes.Unimplemented || outcome.Command != "" {
				t.Fatalf("outcome = %+v, want the unknown-request outcome", outcome)
			}
			if len(h.router.routes) != 0 || len(h.chat.recorded()) != 0 {
				t.Fatalf("routes = %v, chat calls = %d", h.router.routes, len(h.chat.recorded()))
			}
			for _, src := range h.exec.CommandSources() {
				if src.Labels["command"] == name {
					t.Fatalf("%s still has a counter", name)
				}
			}
		})
	}
	if refused != 22 {
		t.Fatalf("refused commands = %d, want 22", refused)
	}
}

func TestCallerComesOnlyFromTheClaims(t *testing.T) {
	h := newHarness(t, time.Second)
	ctx := metadata.AppendToOutgoingContext(t.Context(), chatrpc.TenantHeader, "evil", chatrpc.UserHeader, "mallory")
	resp, _ := h.exec.Execute(ctx, testClaims, sendRequest(&chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Text: "hi"}))
	if resp.GetCode() != 0 {
		t.Fatalf("resp = %v", resp)
	}
	md := h.chat.recorded()[0].md
	if got := md.Get(chatrpc.TenantHeader); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("tenant = %v", got)
	}
	if got := md.Get(chatrpc.UserHeader); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("user = %v", got)
	}
}

func TestSendMessageRefusesInternalFields(t *testing.T) {
	cases := map[string]*chatimv1.SendMessageRequest{
		"system kind":  {RoomId: "42", Cid: "c1", Kind: chatimv1.MessageKind_MESSAGE_KIND_SYSTEM},
		"unknown kind": {RoomId: "42", Cid: "c1", Kind: chatimv1.MessageKind(99)},
		"skip unread":  {RoomId: "42", Cid: "c1", SkipUnread: true},
		"meta":         {RoomId: "42", Cid: "c1", Meta: []byte("{}")},
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, time.Second)
			resp, outcome := h.exec.Execute(t.Context(), testClaims, sendRequest(send))
			if codes.Code(resp.GetCode()) != codes.InvalidArgument || outcome.Code != codes.InvalidArgument || resp.GetMessage() == "" {
				t.Fatalf("resp = %v", resp)
			}
			if resp.GetResult() != nil || len(h.chat.recorded()) != 0 || len(h.router.routes) != 0 {
				t.Fatalf("forwarded: resp %v calls %d routes %v", resp, len(h.chat.recorded()), h.router.routes)
			}
		})
	}
}

func TestSendMessageAcceptsAnExplicitTextKind(t *testing.T) {
	h := newHarness(t, time.Second)
	resp, _ := h.exec.Execute(t.Context(), testClaims, sendRequest(&chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Text: "hi", Kind: chatimv1.MessageKind_MESSAGE_KIND_TEXT}))
	if resp.GetCode() != 0 || len(h.chat.recorded()) != 1 {
		t.Fatalf("resp = %v, chat calls = %d", resp, len(h.chat.recorded()))
	}
}

func TestSendMessageReturnsTheChatResult(t *testing.T) {
	h := newHarness(t, time.Second)
	h.chat.reply(&chatimv1.SendMessageResponse{Seq: 7})
	resp, _ := h.exec.Execute(t.Context(), testClaims, sendRequest(&chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Text: "hi"}))
	if resp.GetSendMessage().GetSeq() != 7 || resp.GetId() != 5 {
		t.Fatalf("resp = %v", resp)
	}
}

func TestEmptyRequestIsUnimplemented(t *testing.T) {
	h := newHarness(t, time.Second)
	resp, outcome := h.exec.Execute(t.Context(), testClaims, &chatimv1.ClientRequest{Id: 3})
	if codes.Code(resp.GetCode()) != codes.Unimplemented || resp.GetId() != 3 || outcome.Command != "" || resp.GetMessage() == "" {
		t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
	}
	if len(h.router.routes) != 0 || len(h.chat.recorded()) != 0 {
		t.Fatal("an empty request reached the router or chat")
	}
}

func TestNewRefusesBadArguments(t *testing.T) {
	if _, err := command.New(nil, time.Second); err == nil {
		t.Fatal("nil router accepted")
	}
	if _, err := command.New(&fakeRouter{}, 0); err == nil {
		t.Fatal("zero deadline accepted")
	}
}
