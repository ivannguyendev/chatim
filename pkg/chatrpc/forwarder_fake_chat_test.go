package chatrpc_test

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

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/grpcclient"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type chatCall struct {
	method string
	md     metadata.MD
	body   []byte
}

type fakeChat struct {
	mu       sync.Mutex
	calls    []chatCall
	err      error
	response proto.Message
	hanging  bool
}

func (f *fakeChat) handle(_ any, stream grpc.ServerStream) error {
	method, _ := grpc.MethodFromServerStream(stream)
	var in emptypb.Empty
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.mu.Lock()
	f.calls = append(f.calls, chatCall{method: method, md: md, body: in.ProtoReflect().GetUnknown()})
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

func (f *fakeChat) set(fn func(*fakeChat)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeChat) recorded() []chatCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chatCall(nil), f.calls...)
}

func startFakeChatNode(t *testing.T) (*fakeChat, chatrpc.Clients) {
	t.Helper()
	chat := &fakeChat{}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(chat.handle))
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(lis)
	}()
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	cc, err := grpcclient.New("passthrough:///chat", grpcclient.Options{Creds: insecure.NewCredentials(), DialOptions: []grpc.DialOption{dialer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cc.Close()
		srv.Stop()
		<-served
	})
	return chat, chatrpc.Clients{Chat: chatimv1.NewChatServiceClient(cc), Mention: chatimv1.NewMentionServiceClient(cc), Media: chatimv1.NewMediaServiceClient(cc)}
}
