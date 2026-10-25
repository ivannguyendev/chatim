package route_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func roomListCalls() map[string]linkCase {
	pinned, name := true, "renamed"
	return map[string]linkCase{
		"list my rooms": linkCaseOf(&chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Next: "n0", Limit: 2}, (*route.Client).ListMyRooms,
			&chatimv1.ListMyRoomsResponse{Items: []*chatimv1.RoomItem{{Room: &chatimv1.Room{Id: "42"}, Unread: &chatimv1.Unread{Count: 3}}}, Next: "n1"}, false),
		"get rooms": linkCaseOf(&chatimv1.GetRoomsRequest{RoomIds: []string{"42", "43"}}, (*route.Client).GetRooms,
			&chatimv1.GetRoomsResponse{Items: []*chatimv1.RoomItem{{Room: &chatimv1.Room{Id: "42"}}}, Missing: []string{"43"}}, false),
		"list my room ids": linkCaseOf(&chatimv1.ListMyRoomIDsRequest{}, (*route.Client).ListMyRoomIDs,
			&chatimv1.ListMyRoomIDsResponse{Rooms: []*chatimv1.MyRoomID{{RoomId: "42", Ver: 3}}}, false),
		"update room": linkCaseOf(&chatimv1.UpdateRoomRequest{RoomId: "42", Name: &name}, (*route.Client).UpdateRoom,
			&chatimv1.UpdateRoomResponse{Room: &chatimv1.Room{Id: "42", Name: name, InfoVer: 2}, Changed: true}, true),
		"set preferences": linkCaseOf(&chatimv1.SetRoomPreferencesRequest{RoomId: "42", Pinned: &pinned}, (*route.Client).SetRoomPreferences,
			&chatimv1.SetRoomPreferencesResponse{Changed: true, State: &chatimv1.MyRoomState{PreferenceVer: 1}}, true),
		"list members": linkCaseOf(&chatimv1.ListMembersRequest{RoomId: "42", After: "a0", Limit: 2}, (*route.Client).ListMembers,
			&chatimv1.ListMembersResponse{Members: []*chatimv1.MemberItem{{User: "alice", Role: chatimv1.MemberRole_MEMBER_ROLE_OWNER}}, Next: "a1"}, true),
		"get messages": linkCaseOf(&chatimv1.GetMessagesRequest{RoomId: "42", Seqs: []uint64{3, 1}}, (*route.Client).GetMessages,
			&chatimv1.GetMessagesResponse{Messages: []*chatimv1.Message{{RoomId: "42", Seq: 1}, {RoomId: "42", Seq: 3}}}, true),
		"list threads": linkCaseOf(&chatimv1.ListThreadsRequest{RoomId: "42", Before: "t0", Limit: 5}, (*route.Client).ListThreads,
			&chatimv1.ListThreadsResponse{Items: []*chatimv1.ThreadItem{{Message: &chatimv1.Message{RoomId: "42", Seq: 1}}}, Next: "t1"}, true),
		"get reactions": linkCaseOf(&chatimv1.GetReactionsRequest{RoomId: "42", Seq: 1, Emoji: "👍", Limit: 5}, (*route.Client).GetReactions,
			&chatimv1.GetReactionsResponse{Reactors: []*chatimv1.Reactor{{User: "bob", Emoji: "👍"}}, Next: "r1"}, true),
		"list pins": linkCaseOf(&chatimv1.ListPinsRequest{RoomId: "42"}, (*route.Client).ListPins,
			&chatimv1.ListPinsResponse{PinVer: 2, Items: []*chatimv1.PinnedMessage{{Pin: &chatimv1.Pin{Seq: 3}, Message: &chatimv1.Message{RoomId: "42", Seq: 3}}}}, true),
	}
}

func TestRoomListAndReadCallsRetryWithTheSameRequestOnTheirCore(t *testing.T) {
	retried := []error{status.Error(codes.Unavailable, "x"), status.Error(codes.Aborted, "x")}
	for name, lc := range roomListCalls() {
		synctest.Test(t, func(t *testing.T) {
			byRoom, anyCore := &fakeCore{block: 1, fail: slices.Clone(retried)}, &fakeCore{block: 1, fail: slices.Clone(retried)}
			net := newFakeNet(map[string]*fakeCore{"core-1:9000": byRoom, "core-2:9000": anyCore})
			c := newClient(t, &splitLocator{}, net, route.Policy{Attempt: time.Second})
			got, st, err := lc.call(t.Context(), c)
			if err != nil || st.Attempts != 4 || !proto.Equal(got, lc.want) {
				t.Fatalf("%s = %v, %v after %d attempts; want %v on the fourth attempt", name, got, err, st.Attempts, lc.want)
			}
			served, idle, wantAddr := anyCore, byRoom, "core-2:9000"
			if lc.inRoom {
				served, idle, wantAddr = byRoom, anyCore, "core-1:9000"
			}
			reqs := served.requests()
			if st.Addr != wantAddr || len(reqs) != 4 || slices.ContainsFunc(reqs, func(r proto.Message) bool { return r != lc.req }) || len(idle.requests()) != 0 {
				t.Fatalf("%s went to %s with %v, want the same request 4 times on %s only", name, st.Addr, reqs, wantAddr)
			}
		})
	}
}

func TestRoomListAndReadCallsKeepRefusalsAndBadRoomIDsLocal(t *testing.T) {
	core := &fakeCore{fail: []error{status.Error(codes.PermissionDenied, "not an owner")}}
	c := newClient(t, &roomLocator{}, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{})
	if _, st, err := roomListCalls()["update room"].call(t.Context(), c); status.Code(err) != codes.PermissionDenied || st.Attempts != 1 {
		t.Fatalf("update by a member = %v after %d attempts, want PermissionDenied at once", err, st.Attempts)
	}
	bad := map[string]func(context.Context) error{
		"update room": func(ctx context.Context) error {
			return errOf(c.UpdateRoom(ctx, &chatimv1.UpdateRoomRequest{RoomId: "x"}))
		},
		"set preferences": func(ctx context.Context) error {
			return errOf(c.SetRoomPreferences(ctx, &chatimv1.SetRoomPreferencesRequest{RoomId: "x"}))
		},
		"list members": func(ctx context.Context) error {
			return errOf(c.ListMembers(ctx, &chatimv1.ListMembersRequest{RoomId: "x"}))
		},
		"get messages": func(ctx context.Context) error {
			return errOf(c.GetMessages(ctx, &chatimv1.GetMessagesRequest{RoomId: "x"}))
		},
		"list threads": func(ctx context.Context) error {
			return errOf(c.ListThreads(ctx, &chatimv1.ListThreadsRequest{RoomId: "x"}))
		},
		"get reactions": func(ctx context.Context) error {
			return errOf(c.GetReactions(ctx, &chatimv1.GetReactionsRequest{RoomId: "x"}))
		},
		"list pins": func(ctx context.Context) error { return errOf(c.ListPins(ctx, &chatimv1.ListPinsRequest{RoomId: "x"})) },
	}
	for name, call := range bad {
		if err := call(t.Context()); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s with a bad room id = %v, want a local InvalidArgument", name, err)
		}
	}
	if n := len(core.requests()); n != 1 {
		t.Fatalf("core saw %d requests, want only the refused update", n)
	}
}

func errOf[T any](_ T, _ route.Stats, err error) error { return err }
