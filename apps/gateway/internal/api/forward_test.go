package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/v1/chatimv1connect"
)

func bearerOf(token string) string { return "Bearer " + token }

func withToken(token string) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", bearerOf(token))
			return next(ctx, req)
		}
	}))
}

func errorCode(t *testing.T, r reply) string {
	t.Helper()
	var body struct{ Code, Message string }
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("status %d body %q: %v", r.status, r.body, err)
	}
	return body.Code
}

func TestEveryRequestCommandReachesChatAsTheTokenUser(t *testing.T) {
	cmds, err := chatrpc.Commands(chatrpc.Request())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, roomy)
	token := h.token(t, claims{tenant: "acme", user: "alice"})
	forged := http.Header{"X-Chatim-Tenant": {"evil"}, "X-Chatim-User": {"mallory"}}
	for i, c := range cmds {
		mt, err := protoregistry.GlobalTypes.FindMessageByName(c.Request.FullName())
		if err != nil {
			t.Fatal(err)
		}
		m := mt.New()
		if c.Room != nil {
			m.Set(c.Room, protoreflect.ValueOfString("42"))
		}
		body, err := proto.Marshal(m.Interface())
		if err != nil {
			t.Fatal(err)
		}
		resp := h.post(t, c.Method, "application/proto", bearerOf(token), body, forged)
		if resp.status != http.StatusOK {
			t.Fatalf("%s status = %d", c.Name, resp.status)
		}
		calls := h.chat.recorded()
		if len(calls) != i+1 || calls[i].method != c.Method {
			t.Fatalf("%s chat calls = %+v", c.Name, calls)
		}
		if got := calls[i].md.Get(chatrpc.TenantHeader); len(got) != 1 || got[0] != "acme" {
			t.Fatalf("%s tenant = %v", c.Name, got)
		}
		if got := calls[i].md.Get(chatrpc.UserHeader); len(got) != 1 || got[0] != "alice" {
			t.Fatalf("%s user = %v", c.Name, got)
		}
	}
}

func TestGeneratedClientsSpeakJSONAndProtobuf(t *testing.T) {
	h := newHarness(t, roomy)
	h.chat.set(func(f *fakeChat) { f.reply = &chatimv1.GetRoomsResponse{Missing: []string{"7"}} })
	token := h.token(t, claims{})
	for name, opts := range map[string][]connect.ClientOption{"proto": nil, "json": {connect.WithProtoJSON()}} {
		client := chatimv1connect.NewChatServiceClient(h.server.Client(), h.server.URL, append(opts, withToken(token))...)
		resp, err := client.GetRooms(t.Context(), connect.NewRequest(&chatimv1.GetRoomsRequest{RoomIds: []string{"7"}}))
		if err != nil || !slices.Equal(resp.Msg.GetMissing(), []string{"7"}) {
			t.Fatalf("%s GetRooms = %v, %v", name, resp, err)
		}
	}
}

func TestUnknownFieldsAreDroppedForJSONAndProtobuf(t *testing.T) {
	h := newHarness(t, roomy)
	token := bearerOf(h.token(t, claims{}))
	known := &chatimv1.GetHistoryRequest{RoomId: "42", Limit: 5}
	raw, err := proto.Marshal(known)
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendVarint(protowire.AppendTag(raw, 99, protowire.VarintType), 1)
	bodies := map[string][]byte{"application/json": []byte(`{"roomId":"42","limit":5,"bogus":true}`), "application/proto": raw}
	for contentType, body := range bodies {
		resp := h.post(t, chatimv1.ChatService_GetHistory_FullMethodName, contentType, token, body, nil)
		if resp.status != http.StatusOK {
			t.Fatalf("%s status = %d", contentType, resp.status)
		}
		calls := h.chat.recorded()
		var got chatimv1.GetHistoryRequest
		if err := proto.Unmarshal(calls[len(calls)-1].body, &got); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(&got, known) || len(got.ProtoReflect().GetUnknown()) != 0 {
			t.Fatalf("%s chat got %v with unknown %x", contentType, &got, got.ProtoReflect().GetUnknown())
		}
	}
}

func TestEveryMethodOutsideRequestIsUnimplemented(t *testing.T) {
	h := newHarness(t, roomy)
	token := bearerOf(h.token(t, claims{}))
	served := chatrpc.Request()
	refused := 0
	for _, service := range []protoreflect.FullName{"chatim.v1.ChatService", "chatim.v1.MentionService", "chatim.v1.MediaService"} {
		d, err := protoregistry.GlobalFiles.FindDescriptorByName(service)
		if err != nil {
			t.Fatal(err)
		}
		methods := d.(protoreflect.ServiceDescriptor).Methods()
		for i := range methods.Len() {
			path := "/" + string(service) + "/" + string(methods.Get(i).Name())
			if slices.Contains(served, path) {
				continue
			}
			refused++
			if code := errorCode(t, h.post(t, path, "application/proto", token, nil, nil)); code != "unimplemented" {
				t.Fatalf("%s code = %q", path, code)
			}
		}
	}
	if refused != 15 || len(h.chat.recorded()) != 0 {
		t.Fatalf("refused = %d, chat calls = %d", refused, len(h.chat.recorded()))
	}
	if got := h.read(t, "commands_total", map[string]string{"command": "unknown", "code": "Unimplemented"}); got != 15 {
		t.Fatalf("unknown counted = %v", got)
	}
}

func TestInternalAndRealtimeCallsAreRefusedOnTheGeneratedClient(t *testing.T) {
	h := newHarness(t, roomy)
	opt := withToken(h.token(t, claims{}))
	chat := chatimv1connect.NewChatServiceClient(h.server.Client(), h.server.URL, opt)
	mention := chatimv1connect.NewMentionServiceClient(h.server.Client(), h.server.URL, opt)
	calls := map[string]error{}
	_, calls["SendMessage"] = chat.SendMessage(t.Context(), connect.NewRequest(&chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1"}))
	_, calls["ListMyRoomIDs"] = chat.ListMyRoomIDs(t.Context(), connect.NewRequest(&chatimv1.ListMyRoomIDsRequest{}))
	_, calls["SetUserGroups"] = mention.SetUserGroups(t.Context(), connect.NewRequest(&chatimv1.SetUserGroupsRequest{}))
	_, calls["SetGroups"] = mention.SetGroups(t.Context(), connect.NewRequest(&chatimv1.SetGroupsRequest{}))
	for name, err := range calls {
		if connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatalf("%s = %v, want unimplemented", name, err)
		}
	}
	if len(h.chat.recorded()) != 0 {
		t.Fatal("a refused call reached chat")
	}
}

func TestChatCodesMapOneToOneWithFixedInfraMessages(t *testing.T) {
	kept := map[codes.Code]bool{codes.InvalidArgument: true, codes.NotFound: true, codes.FailedPrecondition: true, codes.PermissionDenied: true, codes.AlreadyExists: true, codes.ResourceExhausted: true}
	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		t.Run(code.String(), func(t *testing.T) {
			h := newHarness(t, roomy)
			h.chat.set(func(f *fakeChat) { f.err = status.Error(code, "chat detail at 10.9.8.7") })
			client := chatimv1connect.NewChatServiceClient(h.server.Client(), h.server.URL, connect.WithProtoJSON(), withToken(h.token(t, claims{})))
			_, err := client.GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
			ce := new(connect.Error)
			if !errors.As(err, &ce) || int(ce.Code()) != int(code) || !strings.EqualFold(strings.ReplaceAll(ce.Code().String(), "_", ""), code.String()) {
				t.Fatalf("err = %v", err)
			}
			if kept[code] != (ce.Message() == "chat detail at 10.9.8.7") || strings.Contains(ce.Message(), "10.9.8.7") != kept[code] || ce.Message() == "" {
				t.Fatalf("message = %q", ce.Message())
			}
			if len(h.chat.recorded()) != 1 {
				t.Fatalf("chat calls = %d, want exactly 1", len(h.chat.recorded()))
			}
		})
	}
}
