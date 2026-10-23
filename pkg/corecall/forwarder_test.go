package corecall_test

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

	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/corecall"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const sendMethod = chatimv1.CoreService_SendMessage_FullMethodName

var testClaims = jwtverify.Claims{Tenant: "t1", User: "u1"}

type harness struct {
	fwd         *corecall.Forwarder
	core        *fakeCore
	conn        corecall.Conn
	routeErr    error
	routes      []string
	unavailable int
}

func newHarness(t *testing.T, deadline time.Duration, checks map[string]corecall.Check) *harness {
	t.Helper()
	core, conn := startFakeCore(t)
	h := &harness{core: core, conn: conn}
	fwd, err := corecall.New(corecall.Options{
		Methods:  corecall.All(),
		Deadline: deadline,
		Route: func(cmd corecall.Command, req proto.Message) (corecall.Conn, error) {
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

func newRequest(t *testing.T, c corecall.Command) proto.Message {
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
	cmds, err := corecall.Commands(corecall.All())
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
			if calls := h.core.recorded(); len(calls) != 1 || calls[0].method != c.Method {
				t.Fatalf("core calls = %+v", calls)
			}
		})
	}
}

func TestCallerComesOnlyFromTheClaims(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	ctx := metadata.AppendToOutgoingContext(t.Context(), callerid.TenantHeader, "evil", callerid.UserHeader, "mallory")
	if _, outcome := h.fwd.Forward(ctx, testClaims, sendMethod, plainSend()); outcome.Code != codes.OK {
		t.Fatalf("outcome = %+v", outcome)
	}
	md := h.core.recorded()[0].md
	if got := md.Get(callerid.TenantHeader); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("tenant = %v", got)
	}
	if got := md.Get(callerid.UserHeader); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("user = %v", got)
	}
}

func TestUnknownFieldsNeverReachCore(t *testing.T) {
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
	if err := proto.Unmarshal(h.core.recorded()[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ProtoReflect().GetUnknown()) != 0 || len(got.GetMentions().ProtoReflect().GetUnknown()) != 0 {
		t.Fatal("unknown fields reached core")
	}
	if got.GetText() != "hi" || !got.GetMentions().GetAll() || len(send.ProtoReflect().GetUnknown()) == 0 {
		t.Fatalf("core got %v, caller's request changed: %v", &got, len(send.ProtoReflect().GetUnknown()) == 0)
	}
}

func TestCopyKnownDropsUnknownFieldsOnACopy(t *testing.T) {
	in := &chatimv1.MarkReadRequest{RoomId: "42", Seq: 7}
	in.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	out, err := corecall.CopyKnown(in)
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
	codes.DeadlineExceeded: "core did not answer in time",
	codes.Unavailable:      "core unavailable, retry later",
	codes.Unimplemented:    "request not supported",
}

var coreMessageKept = map[codes.Code]bool{
	codes.InvalidArgument: true, codes.NotFound: true, codes.FailedPrecondition: true,
	codes.PermissionDenied: true, codes.AlreadyExists: true, codes.ResourceExhausted: true,
}

func TestCoreErrorsGetAFixedMessageUnlessKept(t *testing.T) {
	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		t.Run(code.String(), func(t *testing.T) {
			h := newHarness(t, time.Second, nil)
			h.core.set(func(f *fakeCore) { f.err = status.Error(code, "core detail at 10.9.8.7") })
			resp, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
			want := fixedMessages[code]
			switch {
			case coreMessageKept[code]:
				want = "core detail at 10.9.8.7"
			case want == "":
				want = "request failed"
			}
			if resp != nil || outcome.Code != code || outcome.Message != want || !strings.Contains(outcome.Detail, "core detail") {
				t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
			}
			if wantHooks := map[bool]int{true: 1}[code == codes.Unavailable]; h.unavailable != wantHooks || len(h.core.recorded()) != 1 {
				t.Fatalf("hooks = %d, calls = %d", h.unavailable, len(h.core.recorded()))
			}
		})
	}
}

func TestRouteErrorsPassThroughWithoutACoreCall(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	h.routeErr = status.Error(codes.Unavailable, "dial core 10.9.8.7:9000: refused")
	_, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
	if outcome.Code != codes.Unavailable || outcome.Detail != h.routeErr.Error() || outcome.Message != "core unavailable, retry later" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if h.unavailable != 1 || len(h.core.recorded()) != 0 {
		t.Fatalf("hooks = %d, calls = %d", h.unavailable, len(h.core.recorded()))
	}
}

func TestCheckRefusesBeforeRouting(t *testing.T) {
	refused := status.Error(codes.InvalidArgument, "kind not allowed")
	h := newHarness(t, time.Second, map[string]corecall.Check{sendMethod: func(proto.Message) error { return refused }})
	_, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
	if outcome.Code != codes.InvalidArgument || outcome.Message != "kind not allowed" || len(h.routes) != 0 || len(h.core.recorded()) != 0 {
		t.Fatalf("outcome = %+v, routes = %v", outcome, h.routes)
	}
	if got := readCount(t, h.fwd, "SendMessage", "InvalidArgument"); got != 1 {
		t.Fatalf("counted = %v", got)
	}
}

func TestCoreCallIsBoundedByTheDeadline(t *testing.T) {
	h := newHarness(t, 50*time.Millisecond, nil)
	h.core.set(func(f *fakeCore) { f.hanging = true })
	_, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, plainSend())
	if outcome.Code != codes.DeadlineExceeded || outcome.Message != "core did not answer in time" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestUnservedMethodAndWrongRequestNeverReachCore(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	_, outcome := h.fwd.Forward(t.Context(), testClaims, chatimv1.CoreService_ListMyRoomIDs_FullMethodName, &chatimv1.ListMyRoomIDsRequest{})
	if outcome.Code != codes.Unimplemented || outcome.Command != "" || outcome.Message != "request not supported" {
		t.Fatalf("unserved outcome = %+v", outcome)
	}
	for _, req := range []proto.Message{nil, &chatimv1.MarkReadRequest{RoomId: "42"}} {
		if _, outcome := h.fwd.Forward(t.Context(), testClaims, sendMethod, req); outcome.Code != codes.Internal {
			t.Fatalf("wrong request outcome = %+v", outcome)
		}
	}
	if len(h.routes) != 0 || len(h.core.recorded()) != 0 {
		t.Fatalf("routes = %v, calls = %d", h.routes, len(h.core.recorded()))
	}
}

func TestMissingMentionClientIsUnimplemented(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	h.conn.Mention = nil
	_, outcome := h.fwd.Forward(t.Context(), testClaims, chatimv1.MentionService_ListMentions_FullMethodName, &chatimv1.ListMentionsRequest{})
	if outcome.Code != codes.Unimplemented || outcome.Detail == "" || len(h.core.recorded()) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestNewRefusesBadOptions(t *testing.T) {
	route := func(corecall.Command, proto.Message) (corecall.Conn, error) {
		return corecall.Conn{}, errors.New("unused")
	}
	never := func(proto.Message) error { return nil }
	for name, opts := range map[string]corecall.Options{
		"no route":       {Methods: corecall.Request(), Deadline: time.Second},
		"zero deadline":  {Methods: corecall.Request(), Route: route},
		"unknown method": {Methods: []string{"/x.Y/Z"}, Deadline: time.Second, Route: route},
		"unserved check": {Methods: corecall.Request(), Deadline: time.Second, Route: route, Checks: map[string]corecall.Check{sendMethod: never}},
	} {
		if _, err := corecall.New(opts); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
