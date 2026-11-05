package route_test

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var unavailable = status.Error(codes.Unavailable, "chat down")

func send(cid string) *chatimv1.SendMessageRequest {
	return &chatimv1.SendMessageRequest{RoomId: "42", Cid: cid, Text: "hello"}
}

func TestSendRetriesOnTheRefreshedRouteWithTheSameCID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dead := &fakeChat{fail: []error{unavailable, unavailable, unavailable}}
		live := &fakeChat{}
		net := newFakeNet(map[string]*fakeChat{"chat-1:9000": dead, "chat-2:9000": live})
		loc := &fakeLocator{route: func(n int) (string, bool) {
			if n < 2 {
				return "chat-1:9000", true
			}
			return "chat-2:9000", true
		}}
		c := newClient(t, loc, net, route.Policy{})
		resp, st, err := c.SendMessage(chatrpc.WithCaller(t.Context(), "acme", "alice"), send("c-1"))
		if err != nil || resp.GetSeq() != 7 {
			t.Fatalf("SendMessage = %v, %v; want seq 7", resp, err)
		}
		if st.Attempts != 3 || st.Addr != "chat-2:9000" || loc.count() != 2 {
			t.Fatalf("stats = %+v after %d refreshes, want 3 attempts ending on chat-2", st, loc.count())
		}
		for _, req := range append(dead.sends, live.sends...) {
			if req.GetCid() != "c-1" || req.GetText() != "hello" {
				t.Fatalf("retried request %v, want the original cid and text", req)
			}
		}
		md := live.callers[0]
		if got := md.Get(chatrpc.TenantHeader); len(got) != 1 || got[0] != "acme" {
			t.Fatalf("tenant metadata = %v", got)
		}
		if got := md.Get(chatrpc.UserHeader); len(got) != 1 || got[0] != "alice" {
			t.Fatalf("user metadata = %v", got)
		}
	})
}

func TestPermanentErrorsReturnAfterOneAttempt(t *testing.T) {
	chat := &fakeChat{fail: []error{status.Error(codes.InvalidArgument, "text")}}
	loc := &fakeLocator{route: fixed("chat-1:9000")}
	c := newClient(t, loc, newFakeNet(map[string]*fakeChat{"chat-1:9000": chat}), route.Policy{})
	_, st, err := c.SendMessage(t.Context(), send("c-1"))
	if status.Code(err) != codes.InvalidArgument || st.Attempts != 1 || loc.count() != 0 {
		t.Fatalf("SendMessage = %v with %+v and %d refreshes, want InvalidArgument at once", err, st, loc.count())
	}
}

func TestSendGivesUpAtTheDeadlineWithTheLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		chat := &fakeChat{fail: make([]error, 1000)}
		for i := range chat.fail {
			chat.fail[i] = unavailable
		}
		loc := &fakeLocator{route: fixed("chat-1:9000")}
		c := newClient(t, loc, newFakeNet(map[string]*fakeChat{"chat-1:9000": chat}), route.Policy{Deadline: 3 * time.Second})
		start := time.Now()
		_, st, err := c.SendMessage(t.Context(), send("c-1"))
		if status.Code(err) != codes.Unavailable || st.Attempts < 4 {
			t.Fatalf("SendMessage = %v after %d attempts, want Unavailable after several", err, st.Attempts)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("gave up after %v, want within the 3s deadline", elapsed)
		}
	})
}

func TestAttemptTimeoutsAreRetriedOnlyForIdempotentCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		chat := &fakeChat{block: 1}
		loc := &fakeLocator{route: fixed("chat-1:9000")}
		c := newClient(t, loc, newFakeNet(map[string]*fakeChat{"chat-1:9000": chat}), route.Policy{Attempt: time.Second})
		if _, st, err := c.SendMessage(t.Context(), send("c-1")); err != nil || st.Attempts != 2 {
			t.Fatalf("SendMessage = %v after %d attempts, want success on the second", err, st.Attempts)
		}
		chat.block = 1
		if _, st, err := c.GetHistory(t.Context(), &chatimv1.GetHistoryRequest{RoomId: "42"}); err != nil || st.Attempts != 2 {
			t.Fatalf("GetHistory = %v after %d attempts, want success on the second", err, st.Attempts)
		}
	})
}

func TestCallsWaitForARouteToAppear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		chat := &fakeChat{}
		loc := &fakeLocator{route: func(n int) (string, bool) { return "chat-1:9000", n > 0 }}
		c := newClient(t, loc, newFakeNet(map[string]*fakeChat{"chat-1:9000": chat}), route.Policy{})
		resp, st, err := c.CreateRoom(t.Context(), &chatimv1.CreateRoomRequest{})
		if err != nil || resp.GetRoom().GetId() != "42" || st.Attempts != 2 {
			t.Fatalf("CreateRoom = %v, %v after %d attempts", resp, err, st.Attempts)
		}
	})
}

func TestConnectionsAreReusedPerAddressAndClosedOnce(t *testing.T) {
	net := newFakeNet(map[string]*fakeChat{"chat-1:9000": {}})
	c, err := route.New(&fakeLocator{route: fixed("chat-1:9000")}, net.dial, route.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, _, err := c.SendMessage(t.Context(), send("c-1")); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(c.Close(), c.Close()); err != nil {
		t.Fatal(err)
	}
	if net.dials["chat-1:9000"] != 1 || net.closed != 1 {
		t.Fatalf("dials = %v, closes = %d; want one of each", net.dials, net.closed)
	}
}

func TestInvalidRoomIDsAndSettingsAreRejectedLocally(t *testing.T) {
	net := newFakeNet(map[string]*fakeChat{})
	c := newClient(t, &fakeLocator{route: fixed("chat-1:9000")}, net, route.Policy{})
	if _, st, err := c.SendMessage(t.Context(), send("c-1")); err == nil || st.Attempts != 1 {
		t.Fatalf("SendMessage to an undialable address = %v after %d attempts, want one failed attempt", err, st.Attempts)
	}
	_, st, err := c.GetHistory(t.Context(), &chatimv1.GetHistoryRequest{RoomId: "not-a-room"})
	if status.Code(err) != codes.InvalidArgument || st.Attempts != 0 {
		t.Fatalf("GetHistory = %v with %+v, want a local InvalidArgument", err, st)
	}
	if _, err := route.New(nil, net.dial, route.Policy{}); err == nil {
		t.Fatal("New accepted a nil locator")
	}
	if _, err := route.New(&fakeLocator{}, nil, route.Policy{}); err == nil {
		t.Fatal("New accepted a nil dialer")
	}
	if _, err := route.New(&fakeLocator{}, net.dial, route.Policy{Attempt: -time.Second}); err == nil {
		t.Fatal("New accepted a negative attempt timeout")
	}
}
