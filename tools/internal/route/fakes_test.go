package route_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"go.uber.org/goleak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var quietLog = slog.New(slog.DiscardHandler)

type fakeLocator struct {
	mu        sync.Mutex
	refreshes int
	route     func(refreshes int) (string, bool)
}

func (l *fakeLocator) Addr(uint64) (string, bool) { return l.AnyAddr() }

func (l *fakeLocator) AnyAddr() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.route(l.refreshes)
}

func (l *fakeLocator) Refresh(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshes++
	return nil
}

func (l *fakeLocator) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.refreshes
}

func fixed(addr string) func(int) (string, bool) {
	return func(int) (string, bool) { return addr, true }
}

type fakeCore struct {
	chatimv1.CoreServiceClient
	mu      sync.Mutex
	fail    []error
	block   int
	sends   []*chatimv1.SendMessageRequest
	seen    []proto.Message
	callers []metadata.MD
}

func (f *fakeCore) next(ctx context.Context) error {
	f.mu.Lock()
	md, _ := metadata.FromOutgoingContext(ctx)
	f.callers = append(f.callers, md)
	blocking := f.block > 0
	if blocking {
		f.block--
	}
	var err error
	if !blocking && len(f.fail) > 0 {
		err, f.fail = f.fail[0], f.fail[1:]
	}
	f.mu.Unlock()
	if blocking {
		<-ctx.Done()
		return status.FromContextError(ctx.Err()).Err()
	}
	return err
}

func (f *fakeCore) CreateRoom(ctx context.Context, in *chatimv1.CreateRoomRequest, _ ...grpc.CallOption) (*chatimv1.CreateRoomResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.CreateRoomResponse{Room: &chatimv1.Room{Id: "42"}}, nil
}

func (f *fakeCore) SendMessage(ctx context.Context, in *chatimv1.SendMessageRequest, _ ...grpc.CallOption) (*chatimv1.SendMessageResponse, error) {
	f.mu.Lock()
	f.sends = append(f.sends, in)
	f.mu.Unlock()
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.SendMessageResponse{Seq: 7}, nil
}

func (f *fakeCore) GetHistory(ctx context.Context, _ *chatimv1.GetHistoryRequest, _ ...grpc.CallOption) (*chatimv1.GetHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.GetHistoryResponse{}, nil
}

type closeCounter struct {
	mu *sync.Mutex
	n  *int
}

func (c closeCounter) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.n++
	return nil
}

type fakeNet struct {
	mu     sync.Mutex
	cores  map[string]*fakeCore
	dials  map[string]int
	closed int
}

func newFakeNet(cores map[string]*fakeCore) *fakeNet {
	return &fakeNet{cores: cores, dials: map[string]int{}}
}

func (n *fakeNet) dial(addr string) (chatimv1.CoreServiceClient, io.Closer, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	core, ok := n.cores[addr]
	if !ok {
		return nil, nil, errors.New("unknown address " + addr)
	}
	n.dials[addr]++
	return core, closeCounter{mu: &n.mu, n: &n.closed}, nil
}

func newClient(t *testing.T, loc route.Locator, net *fakeNet, p route.Policy) *route.Client {
	t.Helper()
	c, err := route.New(loc, net.dial, p)
	if err != nil {
		t.Fatalf("route.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
