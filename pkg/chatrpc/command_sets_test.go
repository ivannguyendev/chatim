package chatrpc_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func clientRequestMethods(t *testing.T) []string {
	t.Helper()
	oneof := (&chatimv1.ClientRequest{}).ProtoReflect().Descriptor().Oneofs().ByName("request")
	cmds, err := chatrpc.Commands(chatrpc.All())
	if err != nil {
		t.Fatal(err)
	}
	byRequest := map[string]string{}
	for _, c := range cmds {
		byRequest[string(c.Request.FullName())] = c.Method
	}
	methods := make([]string, 0, oneof.Fields().Len())
	for i := range oneof.Fields().Len() {
		method, ok := byRequest[string(oneof.Fields().Get(i).Message().FullName())]
		if !ok {
			t.Fatalf("client request %s has no chat call", oneof.Fields().Get(i).Name())
		}
		methods = append(methods, method)
	}
	return methods
}

func httpsOnlyMethods() []string {
	return []string{
		chatimv1.ChatService_GetClientConfig_FullMethodName,
		chatimv1.MediaService_StartUpload_FullMethodName,
		chatimv1.MediaService_GetPartURLs_FullMethodName,
		chatimv1.MediaService_CompleteUpload_FullMethodName,
		chatimv1.MediaService_AbortUpload_FullMethodName,
		chatimv1.MediaService_GetAttachmentURLs_FullMethodName,
		chatimv1.MediaService_ListUploads_FullMethodName,
		chatimv1.MediaService_ListRoomFiles_FullMethodName,
		chatimv1.MediaService_GetPublicURLs_FullMethodName,
	}
}

func TestRealtimeAndRequestSplitEveryClientCall(t *testing.T) {
	realtime, request := chatrpc.Realtime(), chatrpc.Request()
	if len(realtime) != 12 || len(request) != 31 {
		t.Fatalf("realtime = %d, request = %d, want 12 and 31", len(realtime), len(request))
	}
	for _, m := range realtime {
		if slices.Contains(request, m) {
			t.Fatalf("%s is in both sets", m)
		}
	}
	union := slices.Sorted(slices.Values(append(slices.Clone(realtime), request...)))
	client := slices.Sorted(slices.Values(append(clientRequestMethods(t), httpsOnlyMethods()...)))
	if !slices.Equal(union, client) || len(client) != 43 {
		t.Fatalf("realtime + request = %v\nclient calls = %v", union, client)
	}
	if all := slices.Sorted(slices.Values(chatrpc.All())); !slices.Equal(all, union) {
		t.Fatalf("All = %v, want %v", all, union)
	}
}

func TestHTTPSOnlyCallsAreRequestCommands(t *testing.T) {
	cmds, err := chatrpc.Commands(httpsOnlyMethods())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if !slices.Contains(chatrpc.Request(), c.Method) || slices.Contains(chatrpc.Realtime(), c.Method) {
			t.Fatalf("%s: in request = %v, in realtime = %v", c.Method, slices.Contains(chatrpc.Request(), c.Method), slices.Contains(chatrpc.Realtime(), c.Method))
		}
	}
}

func TestInternalCallsAreInNoSet(t *testing.T) {
	internal := []string{
		chatimv1.ChatService_ListMyRoomIDs_FullMethodName,
		chatimv1.MentionService_SetUserGroups_FullMethodName,
		chatimv1.MentionService_SetGroups_FullMethodName,
	}
	for _, m := range internal {
		if slices.Contains(chatrpc.Realtime(), m) || slices.Contains(chatrpc.Request(), m) {
			t.Fatalf("internal call %s is offered to clients", m)
		}
		if _, err := chatrpc.Commands([]string{m}); err == nil {
			t.Fatalf("internal call %s has a command", m)
		}
	}
}

func TestSetsAreFreshCopies(t *testing.T) {
	chatrpc.Realtime()[0] = "changed"
	chatrpc.Request()[0] = "changed"
	if chatrpc.Realtime()[0] == "changed" || chatrpc.Request()[0] == "changed" {
		t.Fatal("a caller changed a shared set")
	}
}
