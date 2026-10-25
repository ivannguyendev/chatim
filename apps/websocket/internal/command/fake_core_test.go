package command_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/grpcclient"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const fakeCoreAddr = "10.9.8.7:9000"

type coreCall struct {
	method string
	md     metadata.MD
	body   []byte
}

type fakeCore struct {
	mu       sync.Mutex
	calls    []coreCall
	err      error
	response proto.Message
	hanging  bool
}

func (f *fakeCore) handle(_ any, stream grpc.ServerStream) error {
	method, _ := grpc.MethodFromServerStream(stream)
	var in emptypb.Empty
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.mu.Lock()
	f.calls = append(f.calls, coreCall{method: method, md: md, body: in.ProtoReflect().GetUnknown()})
	err, reply, hanging := f.err, f.response, f.hanging
	f.mu.Unlock()
	if hanging {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if err != nil {
		return err
	}
	if reply == nil {
		reply = &emptypb.Empty{}
	}
	return stream.SendMsg(reply)
}

func (f *fakeCore) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeCore) hang() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hanging = true
}

func (f *fakeCore) reply(m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response = m
}

func (f *fakeCore) recorded() []coreCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]coreCall(nil), f.calls...)
}

func startFakeCore(t *testing.T) (*fakeCore, coreroute.Conn) {
	t.Helper()
	core := &fakeCore{}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(core.handle))
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(lis)
	}()
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	cc, err := grpcclient.New("passthrough:///core", grpcclient.Options{Creds: insecure.NewCredentials(), DialOptions: []grpc.DialOption{dialer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cc.Close()
		srv.Stop()
		<-served
	})
	return core, coreroute.Conn{Addr: fakeCoreAddr, Core: chatimv1.NewCoreServiceClient(cc), Mention: chatimv1.NewMentionServiceClient(cc)}
}

type fakeRouter struct {
	conn     coreroute.Conn
	err      error
	routes   []string
	triggers int
}

func (r *fakeRouter) Room(roomID string) (coreroute.Conn, error) {
	r.routes = append(r.routes, "room:"+roomID)
	if _, err := coreroute.ParseRoom(roomID); err != nil {
		return coreroute.Conn{}, err
	}
	return r.pick()
}

func (r *fakeRouter) Any() (coreroute.Conn, error) {
	r.routes = append(r.routes, "any")
	return r.pick()
}

func (r *fakeRouter) Trigger() { r.triggers++ }

func (r *fakeRouter) pick() (coreroute.Conn, error) {
	if r.err != nil {
		return coreroute.Conn{}, r.err
	}
	return r.conn, nil
}
