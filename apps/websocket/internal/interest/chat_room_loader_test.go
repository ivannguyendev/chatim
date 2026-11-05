package interest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type roomIDsChat struct {
	chatimv1.ChatServiceClient
	resp     *chatimv1.ListMyRoomIDsResponse
	err      error
	md       metadata.MD
	deadline time.Duration
}

func (c *roomIDsChat) ListMyRoomIDs(ctx context.Context, _ *chatimv1.ListMyRoomIDsRequest, _ ...grpc.CallOption) (*chatimv1.ListMyRoomIDsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	if d, ok := ctx.Deadline(); ok {
		c.deadline = time.Until(d)
	}
	return c.resp, c.err
}

type anyRouter struct {
	chat     *roomIDsChat
	err      error
	triggers int
}

func (r *anyRouter) Any() (chatrpc.PoolConn, error) {
	if r.err != nil {
		return chatrpc.PoolConn{}, r.err
	}
	return chatrpc.PoolConn{Clients: chatrpc.Clients{Chat: r.chat}}, nil
}

func (r *anyRouter) Trigger() { r.triggers++ }

func TestChatLoaderAsksAnyChatNodeAsTheCallerWithinTheDeadline(t *testing.T) {
	chat := &roomIDsChat{resp: &chatimv1.ListMyRoomIDsResponse{
		Rooms: []*chatimv1.MyRoomID{{RoomId: "7", Ver: 3}, {RoomId: "12", Ver: 1}},
		Left:  []*chatimv1.MyRoomID{{RoomId: "9", Ver: 4}},
	}}
	loader, err := NewChatNodeLoader(&anyRouter{chat: chat}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loader.Load(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !slices.Equal(m.Joined, []Room{{ID: 7, Ver: 3}, {ID: 12, Ver: 1}}) || !slices.Equal(m.Left, []Room{{ID: 9, Ver: 4}}) {
		t.Fatalf("membership = %+v", m)
	}
	if got := chat.md.Get(chatrpc.TenantHeader); !slices.Equal(got, []string{"acme"}) {
		t.Fatalf("tenant header = %v", got)
	}
	if got := chat.md.Get(chatrpc.UserHeader); !slices.Equal(got, []string{"alice"}) {
		t.Fatalf("user header = %v", got)
	}
	if chat.deadline <= 0 || chat.deadline > 2*time.Second {
		t.Fatalf("deadline = %v, want within 2s", chat.deadline)
	}
}

func TestChatLoaderFailures(t *testing.T) {
	noRoute := &anyRouter{err: chatrpc.ErrNoRoute}
	if _, err := mustLoader(t, noRoute).Load(context.Background(), "t", "u"); !errors.Is(err, chatrpc.ErrNoRoute) {
		t.Fatalf("no route = %v", err)
	}
	down := &anyRouter{chat: &roomIDsChat{err: status.Error(codes.Unavailable, "down")}}
	if _, err := mustLoader(t, down).Load(context.Background(), "t", "u"); status.Code(err) != codes.Unavailable || down.triggers != 1 {
		t.Fatalf("unavailable = %v, triggers %d", err, down.triggers)
	}
	denied := &anyRouter{chat: &roomIDsChat{err: status.Error(codes.PermissionDenied, "no")}}
	if _, err := mustLoader(t, denied).Load(context.Background(), "t", "u"); err == nil || denied.triggers != 0 {
		t.Fatalf("denied = %v, triggers %d", err, denied.triggers)
	}
	bad := &anyRouter{chat: &roomIDsChat{resp: &chatimv1.ListMyRoomIDsResponse{Rooms: []*chatimv1.MyRoomID{{RoomId: "x"}}}}}
	if _, err := mustLoader(t, bad).Load(context.Background(), "t", "u"); err == nil {
		t.Fatal("a malformed room id was accepted")
	}
	badLeft := &anyRouter{chat: &roomIDsChat{resp: &chatimv1.ListMyRoomIDsResponse{Left: []*chatimv1.MyRoomID{{RoomId: "0"}}}}}
	if _, err := mustLoader(t, badLeft).Load(context.Background(), "t", "u"); err == nil {
		t.Fatal("a malformed left room id was accepted")
	}
	if _, err := NewChatNodeLoader(nil, time.Second); err == nil {
		t.Fatal("nil router accepted")
	}
	if _, err := NewChatNodeLoader(noRoute, 0); err == nil {
		t.Fatal("zero deadline accepted")
	}
}

func mustLoader(t *testing.T, r Router) *ChatLoader {
	t.Helper()
	l, err := NewChatNodeLoader(r, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
