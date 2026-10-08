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

type memberCall struct {
	req  proto.Message
	call func(ctx context.Context, c *route.Client) (proto.Message, route.Stats, error)
	want proto.Message
}

func memberCase[Req, Resp proto.Message](req Req, call func(*route.Client, context.Context, Req) (Resp, route.Stats, error), want Resp) memberCall {
	return memberCall{req: req, want: want, call: func(ctx context.Context, c *route.Client) (proto.Message, route.Stats, error) {
		return reply(call(c, ctx, req))
	}}
}

func memberCalls(room string) map[string]memberCall {
	return map[string]memberCall{
		"add": memberCase(&chatimv1.AddMembersRequest{RoomId: room, Users: []string{"bob", "carol"}, RequestId: "add-1"}, (*route.Client).AddMembers,
			&chatimv1.AddMembersResponse{Added: []*chatimv1.AddedMember{{User: "bob", Ver: 1}, {User: "carol", Ver: 1}}}),
		"remove": memberCase(&chatimv1.RemoveMemberRequest{RoomId: room, User: "carol"}, (*route.Client).RemoveMember,
			&chatimv1.RemoveMemberResponse{Changed: true, Ver: 2}),
		"leave": memberCase(&chatimv1.LeaveRoomRequest{RoomId: room}, (*route.Client).LeaveRoom,
			&chatimv1.LeaveRoomResponse{Changed: true, Ver: 2, NewOwner: "bob"}),
		"role": memberCase(&chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}, (*route.Client).ChangeMemberRole,
			&chatimv1.ChangeMemberRoleResponse{Changed: true, Ver: 2, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER}),
		"priority": memberCase(&chatimv1.SetMemberPriorityRequest{RoomId: room, User: "bob", Priority: 5}, (*route.Client).SetMemberPriority,
			&chatimv1.SetMemberPriorityResponse{Changed: true, Ver: 3}),
		"read": memberCase(&chatimv1.MarkReadRequest{RoomId: room, Seq: 9}, (*route.Client).MarkRead,
			&chatimv1.MarkReadResponse{ReadSeq: 9, ReadVer: 2}),
		"unread": memberCase(&chatimv1.MarkUnreadRequest{RoomId: room, Seq: 9}, (*route.Client).MarkUnread,
			&chatimv1.MarkUnreadResponse{ReadSeq: 8, ReadVer: 3}),
	}
}

func TestMemberCallsRouteByRoomAndRetryWithTheSameRequest(t *testing.T) {
	retried := []error{status.Error(codes.Unavailable, "x"), status.Error(codes.ResourceExhausted, "x"), status.Error(codes.Aborted, "x")}
	for name, mc := range memberCalls("42") {
		synctest.Test(t, func(t *testing.T) {
			core := &fakeCore{block: 1, fail: slices.Clone(retried)}
			loc := &roomLocator{}
			c := newClient(t, loc, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{Attempt: time.Second})
			got, st, err := mc.call(t.Context(), c)
			if err != nil || st.Attempts != 5 || !proto.Equal(got, mc.want) {
				t.Fatalf("%s = %v, %v after %d attempts; want %v on the fifth attempt", name, got, err, st.Attempts, mc.want)
			}
			reqs := core.requests()
			if len(reqs) != 5 || slices.ContainsFunc(reqs, func(r proto.Message) bool { return r != mc.req }) {
				t.Fatalf("%s sent %v, want the same request on all 5 attempts", name, reqs)
			}
			if rooms := loc.asked(); len(rooms) != 5 || slices.ContainsFunc(rooms, func(r uint64) bool { return r != 42 }) {
				t.Fatalf("%s asked the locator for rooms %v, want room 42 on every attempt", name, rooms)
			}
		})
	}
}

func TestMemberCallsKeepRefusalsAndBadRoomIDsLocal(t *testing.T) {
	for name, mc := range memberCalls("42") {
		core := &fakeCore{fail: []error{status.Error(codes.FailedPrecondition, "direct room")}}
		c := newClient(t, &roomLocator{}, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{})
		if _, st, err := mc.call(t.Context(), c); status.Code(err) != codes.FailedPrecondition || st.Attempts != 1 {
			t.Fatalf("%s refused = %v after %d attempts, want FailedPrecondition at once", name, err, st.Attempts)
		}
	}
	for name, mc := range memberCalls("not-a-room") {
		c := newClient(t, &roomLocator{}, newFakeNet(map[string]*fakeCore{"core-1:9000": {}}), route.Policy{})
		if _, st, err := mc.call(t.Context(), c); status.Code(err) != codes.InvalidArgument || st.Attempts != 0 {
			t.Fatalf("%s with a bad room id = %v with %+v, want a local InvalidArgument", name, err, st)
		}
	}
}
