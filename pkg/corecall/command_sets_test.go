package corecall_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/corecall"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func clientRequestMethods(t *testing.T) []string {
	t.Helper()
	oneof := (&chatimv1.ClientRequest{}).ProtoReflect().Descriptor().Oneofs().ByName("request")
	cmds, err := corecall.Commands(corecall.All())
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
			t.Fatalf("client request %s has no core call", oneof.Fields().Get(i).Name())
		}
		methods = append(methods, method)
	}
	return methods
}

func TestRealtimeAndRequestSplitEveryClientCall(t *testing.T) {
	realtime, request := corecall.Realtime(), corecall.Request()
	if len(realtime) != 12 || len(request) != 22 {
		t.Fatalf("realtime = %d, request = %d, want 12 and 22", len(realtime), len(request))
	}
	for _, m := range realtime {
		if slices.Contains(request, m) {
			t.Fatalf("%s is in both sets", m)
		}
	}
	union := slices.Sorted(slices.Values(append(slices.Clone(realtime), request...)))
	client := slices.Sorted(slices.Values(clientRequestMethods(t)))
	if !slices.Equal(union, client) || len(client) != 34 {
		t.Fatalf("realtime + request = %v\nclient calls = %v", union, client)
	}
	if all := slices.Sorted(slices.Values(corecall.All())); !slices.Equal(all, union) {
		t.Fatalf("All = %v, want %v", all, union)
	}
}

func TestInternalCallsAreInNoSet(t *testing.T) {
	internal := []string{
		chatimv1.CoreService_ListMyRoomIDs_FullMethodName,
		chatimv1.MentionService_SetUserGroups_FullMethodName,
		chatimv1.MentionService_SetGroups_FullMethodName,
	}
	for _, m := range internal {
		if slices.Contains(corecall.Realtime(), m) || slices.Contains(corecall.Request(), m) {
			t.Fatalf("internal call %s is offered to clients", m)
		}
		if _, err := corecall.Commands([]string{m}); err == nil {
			t.Fatalf("internal call %s has a command", m)
		}
	}
}

func TestSetsAreFreshCopies(t *testing.T) {
	corecall.Realtime()[0] = "changed"
	corecall.Request()[0] = "changed"
	if corecall.Realtime()[0] == "changed" || corecall.Request()[0] == "changed" {
		t.Fatal("a caller changed a shared set")
	}
}
