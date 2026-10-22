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

	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type roomIDsCore struct {
	chatimv1.CoreServiceClient
	resp     *chatimv1.ListMyRoomIDsResponse
	err      error
	md       metadata.MD
	deadline time.Duration
}

func (c *roomIDsCore) ListMyRoomIDs(ctx context.Context, _ *chatimv1.ListMyRoomIDsRequest, _ ...grpc.CallOption) (*chatimv1.ListMyRoomIDsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	if d, ok := ctx.Deadline(); ok {
		c.deadline = time.Until(d)
	}
	return c.resp, c.err
}

type anyRouter struct {
	core     *roomIDsCore
	err      error
	triggers int
}

func (r *anyRouter) Any() (coreroute.Conn, error) {
	if r.err != nil {
		return coreroute.Conn{}, r.err
	}
	return coreroute.Conn{Core: r.core}, nil
}

func (r *anyRouter) Trigger() { r.triggers++ }

func TestCoreLoaderAsksAnyCoreAsTheCallerWithinTheDeadline(t *testing.T) {
	core := &roomIDsCore{resp: &chatimv1.ListMyRoomIDsResponse{
		Rooms: []*chatimv1.MyRoomID{{RoomId: "7", Ver: 3}, {RoomId: "12", Ver: 1}},
		Left:  []*chatimv1.MyRoomID{{RoomId: "9", Ver: 4}},
	}}
	loader, err := NewCoreLoader(&anyRouter{core: core}, 2*time.Second)
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
	if got := core.md.Get(callerid.TenantHeader); !slices.Equal(got, []string{"acme"}) {
		t.Fatalf("tenant header = %v", got)
	}
	if got := core.md.Get(callerid.UserHeader); !slices.Equal(got, []string{"alice"}) {
		t.Fatalf("user header = %v", got)
	}
	if core.deadline <= 0 || core.deadline > 2*time.Second {
		t.Fatalf("deadline = %v, want within 2s", core.deadline)
	}
}

func TestCoreLoaderFailures(t *testing.T) {
	noRoute := &anyRouter{err: coreroute.ErrNoRoute}
	if _, err := mustLoader(t, noRoute).Load(context.Background(), "t", "u"); !errors.Is(err, coreroute.ErrNoRoute) {
		t.Fatalf("no route = %v", err)
	}
	down := &anyRouter{core: &roomIDsCore{err: status.Error(codes.Unavailable, "down")}}
	if _, err := mustLoader(t, down).Load(context.Background(), "t", "u"); status.Code(err) != codes.Unavailable || down.triggers != 1 {
		t.Fatalf("unavailable = %v, triggers %d", err, down.triggers)
	}
	denied := &anyRouter{core: &roomIDsCore{err: status.Error(codes.PermissionDenied, "no")}}
	if _, err := mustLoader(t, denied).Load(context.Background(), "t", "u"); err == nil || denied.triggers != 0 {
		t.Fatalf("denied = %v, triggers %d", err, denied.triggers)
	}
	bad := &anyRouter{core: &roomIDsCore{resp: &chatimv1.ListMyRoomIDsResponse{Rooms: []*chatimv1.MyRoomID{{RoomId: "x"}}}}}
	if _, err := mustLoader(t, bad).Load(context.Background(), "t", "u"); err == nil {
		t.Fatal("a malformed room id was accepted")
	}
	badLeft := &anyRouter{core: &roomIDsCore{resp: &chatimv1.ListMyRoomIDsResponse{Left: []*chatimv1.MyRoomID{{RoomId: "0"}}}}}
	if _, err := mustLoader(t, badLeft).Load(context.Background(), "t", "u"); err == nil {
		t.Fatal("a malformed left room id was accepted")
	}
	if _, err := NewCoreLoader(nil, time.Second); err == nil {
		t.Fatal("nil router accepted")
	}
	if _, err := NewCoreLoader(noRoute, 0); err == nil {
		t.Fatal("zero deadline accepted")
	}
}

func mustLoader(t *testing.T, r Router) *CoreLoader {
	t.Helper()
	l, err := NewCoreLoader(r, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
