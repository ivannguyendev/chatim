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
	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type linkCase struct {
	req    proto.Message
	call   func(ctx context.Context, c *route.Client) (proto.Message, route.Stats, error)
	want   proto.Message
	inRoom bool
}

func linkCaseOf[Req, Resp proto.Message](req Req, do func(*route.Client, context.Context, Req) (Resp, route.Stats, error), want Resp, inRoom bool) linkCase {
	return linkCase{req: req, want: want, inRoom: inRoom, call: func(ctx context.Context, c *route.Client) (proto.Message, route.Stats, error) {
		return reply(do(c, ctx, req))
	}}
}

func linkCalls() map[string]linkCase {
	since := timestamppb.New(time.UnixMilli(7))
	return map[string]linkCase{
		"bookmark": linkCaseOf(&chatimv1.SetBookmarkRequest{RoomId: "42", Seq: 3, On: true}, (*route.Client).SetBookmark,
			&chatimv1.SetBookmarkResponse{Changed: true}, true),
		"replies": linkCaseOf(&chatimv1.GetRepliesRequest{RoomId: "42", Seq: 3, Limit: 10}, (*route.Client).GetReplies,
			&chatimv1.GetRepliesResponse{Messages: []*chatimv1.Message{{RoomId: "42", Seq: 4, ReplyTo: &chatimv1.ReplyRef{Seq: 3}}}, Next: 4}, true),
		"bookmarks": linkCaseOf(&chatimv1.ListBookmarksRequest{Before: "b0", Limit: 5}, (*route.Client).ListBookmarks,
			&chatimv1.ListBookmarksResponse{Items: []*chatimv1.BookmarkItem{{Message: &chatimv1.Message{RoomId: "42", Seq: 3}, Available: true}}, Next: "b1"}, false),
		"mentions": linkCaseOf(&chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: "team", Since: since}}, Limit: 5}, (*route.Client).ListMentions,
			&chatimv1.ListMentionsResponse{Messages: []*chatimv1.Message{{RoomId: "42", Seq: 4, MentionAll: true}}, Next: "m1"}, false),
		"create": linkCaseOf(&chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Members: []string{"bob"}, RequestId: "req-1"}, (*route.Client).CreateRoom,
			&chatimv1.CreateRoomResponse{Room: &chatimv1.Room{Id: "42"}}, false),
	}
}

type splitLocator struct {
	roomLocator
}

func (l *splitLocator) AnyAddr() (string, bool) { return "core-2:9000", true }

func TestLinkCallsRetryAttemptTimeoutsWithTheSameRequest(t *testing.T) {
	retried := []error{status.Error(codes.Unavailable, "x"), status.Error(codes.ResourceExhausted, "x")}
	for name, lc := range linkCalls() {
		synctest.Test(t, func(t *testing.T) {
			byRoom, anyCore := &fakeCore{block: 1, fail: slices.Clone(retried)}, &fakeCore{block: 1, fail: slices.Clone(retried)}
			loc := &splitLocator{}
			net := newFakeNet(map[string]*fakeCore{"core-1:9000": byRoom, "core-2:9000": anyCore})
			c := newClient(t, loc, net, route.Policy{Attempt: time.Second})
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

func TestLinkCallsKeepRefusalsAndBadRoomIDsLocal(t *testing.T) {
	core := &fakeCore{fail: []error{status.Error(codes.FailedPrecondition, "message deleted")}}
	c := newClient(t, &roomLocator{}, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{})
	if _, st, err := linkCalls()["bookmark"].call(t.Context(), c); status.Code(err) != codes.FailedPrecondition || st.Attempts != 1 {
		t.Fatalf("bookmark of a deleted message = %v after %d attempts, want FailedPrecondition at once", err, st.Attempts)
	}
	bad := map[string]func() error{
		"bookmark": func() error {
			_, _, err := c.SetBookmark(t.Context(), &chatimv1.SetBookmarkRequest{RoomId: "not-a-room", Seq: 3})
			return err
		},
		"replies": func() error {
			_, _, err := c.GetReplies(t.Context(), &chatimv1.GetRepliesRequest{RoomId: "not-a-room", Seq: 3})
			return err
		},
	}
	for name, call := range bad {
		if err := call(); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s with a bad room id = %v, want a local InvalidArgument", name, err)
		}
	}
}
