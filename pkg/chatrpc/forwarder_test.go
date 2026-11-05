package chatrpc_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const sendMethod = chatimv1.ChatService_SendMessage_FullMethodName

var testClaims = auth.Claims{Tenant: "t1", User: "u1"}

type harness struct {
	fwd         *chatrpc.Forwarder
	chat        *fakeChat
	conn        chatrpc.Clients
	routeErr    error
	routes      []string
	unavailable int
}

func newHarness(t *testing.T, deadline time.Duration, checks map[string]chatrpc.Check) *harness {
	t.Helper()
	chat, conn := startFakeChatNode(t)
	h := &harness{chat: chat, conn: conn}
	fwd, err := chatrpc.NewForwarder(chatrpc.ForwarderOptions{
		Methods:  chatrpc.All(),
		Deadline: deadline,
		Route: func(cmd chatrpc.Command, req proto.Message) (chatrpc.Clients, error) {
			room, _ := cmd.RoomID(req)
			h.routes = append(h.routes, cmd.Name+":"+room)
			return h.conn, h.routeErr
		},
		Checks:        checks,
		OnUnavailable: func() { h.unavailable++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.fwd = fwd
	return h
}

func plainSend() *chatimv1.SendMessageRequest {
	return &chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Text: "hi"}
}

func newRequest(t *testing.T, c chatrpc.Command) proto.Message {
	t.Helper()
	mt, err := protoregistry.GlobalTypes.FindMessageByName(c.Request.FullName())
	if err != nil {
		t.Fatal(err)
	}
	m := mt.New()
	if c.Room != nil {
		m.Set(c.Room, protoreflect.ValueOfString("42"))
	}
	return m.Interface()
}

func TestEveryCommandCallsItsMethodOnce(t *testing.T) {
	cmds, err := chatrpc.Commands(chatrpc.All())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		t.Run(c.Name, func(t *testing.T) {
			h := newHarness(t, time.Second, nil)
			resp, outcome := h.fwd.Forward(t.Context(), testClaims, c.Method, newRequest(t, c))
			if outcome.Code != codes.OK || outcome.Command != c.Name || resp.ProtoReflect().Descriptor().FullName() != c.Response.FullName() {
				t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
			}
			want := c.Name + ":"
			if c.Room != nil {
				want += "42"
			}
			if len(h.routes) != 1 || h.routes[0] != want {
				t.Fatalf("routes = %v, want %s", h.routes, want)
			}
			if calls := h.chat.recorded(); len(calls) != 1 || calls[0].method != c.Method {
				t.Fatalf("chat calls = %+v", calls)
			}
		})
	}
}

func TestCallerComesOnlyFromTheClaims(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	ctx := metadata.AppendToOutgoingContext(t.Context(), chatrpc.TenantHeader, "evil", chatrpc.UserHeader, "mallory")
	if _, outcome := h.fwd.Forward(ctx, testClaims, sendMethod, plainSend()); outcome.Code != codes.OK {
		t.Fatalf("outcome = %+v", outcome)
	}
	md := h.chat.recorded()[0].md
	if got := md.Get(chatrpc.TenantHeader); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("tenant = %v", got)
	}
	if got := md.Get(chatrpc.UserHeader); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("user = %v", got)
	}
}

func TestUnknownFieldsNeverReachChat(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	send := plainSend()
	send.Mentions = &chatimv1.MentionSet{All: true}
	stranger := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	send.ProtoReflect().SetUnknown(stranger)
	send.GetMentions().ProtoReflect().SetUnknown(stranger)
	if _, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, send); outcome.Code != codes.OK {
		t.Fatalf("outcome = %+v", outcome)
	}
	var got chatimv1.SendMessageRequest
	if err := proto.Unmarshal(h.chat.recorded()[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ProtoReflect().GetUnknown()) != 0 || len(got.GetMentions().ProtoReflect().GetUnknown()) != 0 {
		t.Fatal("unknown fields reached chat")
	}
	if got.GetText() != "hi" || !got.GetMentions().GetAll() || len(send.ProtoReflect().GetUnknown()) == 0 {
		t.Fatalf("chat got %v, caller's request changed: %v", &got, len(send.ProtoReflect().GetUnknown()) == 0)
	}
}

func TestCopyKnownDropsUnknownFieldsOnACopy(t *testing.T) {
	in := &chatimv1.MarkReadRequest{RoomId: "42", Seq: 7}
	in.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	out, err := chatrpc.CopyKnown(in)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.(*chatimv1.MarkReadRequest)
	if !ok || got.GetRoomId() != "42" || got.GetSeq() != 7 || len(got.ProtoReflect().GetUnknown()) != 0 || len(in.ProtoReflect().GetUnknown()) == 0 {
		t.Fatalf("copy = %v", out)
	}
}

var fixedMessages = map[codes.Code]string{
	codes.Canceled:         "request canceled",
	codes.DeadlineExceeded: "chat did not answer in time",
	codes.Unavailable:      "chat unavailable, retry later",
	codes.Unimplemented:    "request not supported",
}

var chatMessageKept = map[codes.Code]bool{
	codes.InvalidArgument: true, codes.NotFound: true, codes.FailedPrecondition: true,
	codes.PermissionDenied: true, codes.AlreadyExists: true, codes.ResourceExhausted: true,
}

func TestChatErrorsGetAFixedMessageUnlessKept(t *testing.T) {
	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		t.Run(code.String(), func(t *testing.T) {
			h := newHarness(t, time.Second, nil)
			h.chat.set(func(f *fakeChat) { f.err = status.Error(code, "chat detail at 10.9.8.7") })
			resp, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
			want := fixedMessages[code]
			switch {
			case chatMessageKept[code]:
				want = "chat detail at 10.9.8.7"
			case want == "":
				want = "request failed"
			}
			if resp != nil || outcome.Code != code || outcome.Message != want || !strings.Contains(outcome.Detail, "chat detail") {
				t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
			}
			if wantHooks := map[bool]int{true: 1}[code == codes.Unavailable]; h.unavailable != wantHooks || len(h.chat.recorded()) != 1 {
				t.Fatalf("hooks = %d, calls = %d", h.unavailable, len(h.chat.recorded()))
			}
		})
	}
}

func TestRouteErrorsPassThroughWithoutAChatNodeCall(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	h.routeErr = status.Error(codes.Unavailable, "dial chat 10.9.8.7:9000: refused")
	_, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
	if outcome.Code != codes.Unavailable || outcome.Detail != h.routeErr.Error() || outcome.Message != "chat unavailable, retry later" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if h.unavailable != 1 || len(h.chat.recorded()) != 0 {
		t.Fatalf("hooks = %d, calls = %d", h.unavailable, len(h.chat.recorded()))
	}
}

func TestCheckRefusesBeforeRouting(t *testing.T) {
	refused := status.Error(codes.InvalidArgument, "kind not allowed")
	h := newHarness(t, time.Second, map[string]chatrpc.Check{sendMethod: func(proto.Message) error { return refused }})
	_, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
	if outcome.Code != codes.InvalidArgument || outcome.Message != "kind not allowed" || len(h.routes) != 0 || len(h.chat.recorded()) != 0 {
		t.Fatalf("outcome = %+v, routes = %v", outcome, h.routes)
	}
	if got := readCount(t, h.fwd, "SendMessage", "InvalidArgument"); got != 1 {
		t.Fatalf("counted = %v", got)
	}
}

func TestChatCallIsBoundedByTheDeadline(t *testing.T) {
	h := newHarness(t, 50*time.Millisecond, nil)
	h.chat.set(func(f *fakeChat) { f.hanging = true })
	_, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
	if outcome.Code != codes.DeadlineExceeded || outcome.Message != "chat did not answer in time" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestUnservedMethodAndWrongRequestNeverReachChat(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	_, outcome := h.fwd.Forward(t.Context(), testClaims, chatimv1.ChatService_ListMyRoomIDs_FullMethodName, &chatimv1.ListMyRoomIDsRequest{})
	if outcome.Code != codes.Unimplemented || outcome.Command != "" || outcome.Message != "request not supported" {
		t.Fatalf("unserved outcome = %+v", outcome)
	}
	for _, req := range []proto.Message{nil, &chatimv1.MarkReadRequest{RoomId: "42"}} {
		if _, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, req); outcome.Code != codes.Internal {
			t.Fatalf("wrong request outcome = %+v", outcome)
		}
	}
	if len(h.routes) != 0 || len(h.chat.recorded()) != 0 {
		t.Fatalf("routes = %v, calls = %d", h.routes, len(h.chat.recorded()))
	}
}

func TestMissingMentionClientIsUnimplemented(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	h.conn.Mention = nil
	_, outcome := h.fwd.Forward(t.Context(), testClaims, chatimv1.MentionService_ListMentions_FullMethodName, &chatimv1.ListMentionsRequest{})
	if outcome.Code != codes.Unimplemented || outcome.Detail == "" || len(h.chat.recorded()) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestNewRefusesBadOptions(t *testing.T) {
	route := func(chatrpc.Command, proto.Message) (chatrpc.Clients, error) {
		return chatrpc.Clients{}, errors.New("unused")
	}
	never := func(proto.Message) error { return nil }
	for name, opts := range map[string]chatrpc.ForwarderOptions{
		"no route":       {Methods: chatrpc.Request(), Deadline: time.Second},
		"zero deadline":  {Methods: chatrpc.Request(), Route: route},
		"unknown method": {Methods: []string{"/x.Y/Z"}, Deadline: time.Second, Route: route},
		"unserved check": {Methods: chatrpc.Request(), Deadline: time.Second, Route: route, Checks: map[string]chatrpc.Check{sendMethod: never}},
	} {
		if _, err := chatrpc.NewForwarder(opts); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
