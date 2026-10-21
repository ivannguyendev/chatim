package command_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var coreMessageKept = map[codes.Code]bool{
	codes.InvalidArgument: true, codes.NotFound: true, codes.FailedPrecondition: true,
	codes.PermissionDenied: true, codes.AlreadyExists: true, codes.ResourceExhausted: true,
}

func plainSend() *chatimv1.ClientRequest {
	return sendRequest(&chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Text: "hi"})
}

func assertClientSafe(t *testing.T, resp *chatimv1.ServerResponse) {
	t.Helper()
	for _, leak := range []string{"call /", "10.9.8.7", fakeCoreAddr} {
		if strings.Contains(resp.GetMessage(), leak) {
			t.Fatalf("message %q leaks %q", resp.GetMessage(), leak)
		}
	}
}

func TestCoreCodesReachTheClientOnce(t *testing.T) {
	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		t.Run(code.String(), func(t *testing.T) {
			h := newHarness(t, time.Second)
			h.core.fail(status.Error(code, "core detail"))
			resp, outcome := h.exec.Execute(t.Context(), testClaims, plainSend())
			if codes.Code(resp.GetCode()) != code || outcome.Code != code || resp.GetResult() != nil {
				t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
			}
			if kept := resp.GetMessage() == "core detail"; kept != coreMessageKept[code] || resp.GetMessage() == "" {
				t.Fatalf("message = %q", resp.GetMessage())
			}
			if !strings.Contains(outcome.Detail, "core detail") {
				t.Fatalf("detail = %q", outcome.Detail)
			}
			assertClientSafe(t, resp)
			if calls := h.core.recorded(); len(calls) != 1 {
				t.Fatalf("core calls = %d, want 1", len(calls))
			}
			if wantTriggers := map[bool]int{true: 1}[code == codes.Unavailable]; h.router.triggers != wantTriggers {
				t.Fatalf("triggers = %d, want %d", h.router.triggers, wantTriggers)
			}
		})
	}
}

func TestBadRoomIsInvalidArgument(t *testing.T) {
	h := newHarness(t, time.Second)
	resp, _ := h.exec.Execute(t.Context(), testClaims, sendRequest(&chatimv1.SendMessageRequest{RoomId: "abc", Cid: "c1"}))
	if codes.Code(resp.GetCode()) != codes.InvalidArgument || len(h.core.recorded()) != 0 || h.router.triggers != 0 {
		t.Fatalf("resp = %v", resp)
	}
}

func TestHugeBadRoomGetsAShortFixedMessage(t *testing.T) {
	h := newHarness(t, time.Second)
	room := strings.Repeat("x", 100<<10)
	resp, outcome := h.exec.Execute(t.Context(), testClaims, sendRequest(&chatimv1.SendMessageRequest{RoomId: room, Cid: "c1"}))
	if codes.Code(resp.GetCode()) != codes.InvalidArgument || resp.GetMessage() != "invalid room_id" {
		t.Fatalf("code = %d, message length = %d", resp.GetCode(), len(resp.GetMessage()))
	}
	if !strings.Contains(outcome.Detail, room) || len(h.router.routes) != 0 || len(h.core.recorded()) != 0 {
		t.Fatalf("detail length = %d, routes = %d, calls = %d", len(outcome.Detail), len(h.router.routes), len(h.core.recorded()))
	}
}

func TestNoLiveCoreIsUnavailableAndRefreshesRoutes(t *testing.T) {
	routeErrors := []error{
		coreroute.ErrNoRoute,
		status.Error(codes.Unavailable, fmt.Sprintf("dial core %s: connection refused", fakeCoreAddr)),
	}
	for _, routeErr := range routeErrors {
		h := newHarness(t, time.Second)
		h.router.err = routeErr
		resp, outcome := h.exec.Execute(t.Context(), testClaims, plainSend())
		if codes.Code(resp.GetCode()) != codes.Unavailable || h.router.triggers != 1 || len(h.core.recorded()) != 0 {
			t.Fatalf("resp = %v, triggers = %d", resp, h.router.triggers)
		}
		if outcome.Detail != routeErr.Error() {
			t.Fatalf("detail = %q", outcome.Detail)
		}
		assertClientSafe(t, resp)
	}
}

func TestCoreCallIsBoundedByTheDeadline(t *testing.T) {
	h := newHarness(t, 50*time.Millisecond)
	h.core.hang()
	resp, outcome := h.exec.Execute(t.Context(), testClaims, plainSend())
	if codes.Code(resp.GetCode()) != codes.DeadlineExceeded || outcome.Code != codes.DeadlineExceeded {
		t.Fatalf("resp = %v", resp)
	}
	assertClientSafe(t, resp)
}

func TestUnknownFieldsNeverReachCore(t *testing.T) {
	h := newHarness(t, time.Second)
	send := &chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Text: "hi", Mentions: &chatimv1.MentionSet{All: true}}
	stranger := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	send.ProtoReflect().SetUnknown(stranger)
	send.GetMentions().ProtoReflect().SetUnknown(stranger)
	resp, _ := h.exec.Execute(t.Context(), testClaims, sendRequest(send))
	if resp.GetCode() != 0 {
		t.Fatalf("resp = %v", resp)
	}
	var got chatimv1.SendMessageRequest
	if err := proto.Unmarshal(h.core.recorded()[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ProtoReflect().GetUnknown()) != 0 || len(got.GetMentions().ProtoReflect().GetUnknown()) != 0 {
		t.Fatal("unknown fields reached core")
	}
	if got.GetText() != "hi" || !got.GetMentions().GetAll() {
		t.Fatalf("known fields lost: %v", &got)
	}
	if len(send.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("the caller's request was changed")
	}
}
