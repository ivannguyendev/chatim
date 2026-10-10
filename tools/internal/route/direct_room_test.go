package route_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func (f *fakeCore) OpenDirectRoom(ctx context.Context, in *chatimv1.OpenDirectRoomRequest, _ ...grpc.CallOption) (*chatimv1.OpenDirectRoomResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.OpenDirectRoomResponse{Room: &chatimv1.Room{Id: "8812", Type: chatimv1.RoomType_ROOM_TYPE_DM}, Created: in.GetOtherUser() == "minh"}, nil
}

func TestOpenDirectRoomGoesToAnyCoreAndRetriesAttemptTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core := &fakeCore{block: 1}
		loc := &fakeLocator{route: func(n int) (string, bool) { return "core-1:9000", n > 0 }}
		c := newClient(t, loc, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{Attempt: time.Second})
		resp, st, err := c.OpenDirectRoom(t.Context(), &chatimv1.OpenDirectRoomRequest{OtherUser: "minh"})
		if err != nil || st.Attempts != 3 || st.Addr != "core-1:9000" || resp.GetRoom().GetId() != "8812" || !resp.GetCreated() {
			t.Fatalf("OpenDirectRoom = %v, %v after %d attempts on %q; want dm 8812 created on the third attempt", resp, err, st.Attempts, st.Addr)
		}
	})
}
