package grpcclient

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const coreMethod = "/chatim.v1.CoreService/GetRooms"

type countingCore struct {
	addr   string
	srv    *grpc.Server
	done   chan struct{}
	calls  atomic.Int32
	hang   atomic.Bool
	gotOne chan *countingCore
}

func startCountingCore(t *testing.T, gotOne chan *countingCore) *countingCore {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &countingCore{addr: lis.Addr().String(), done: make(chan struct{}), gotOne: gotOne}
	c.srv = grpc.NewServer(grpc.UnknownServiceHandler(c.handle))
	go func() {
		defer close(c.done)
		_ = c.srv.Serve(lis)
	}()
	t.Cleanup(c.stop)
	return c
}

func (c *countingCore) handle(_ any, stream grpc.ServerStream) error {
	var in emptypb.Empty
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}
	c.calls.Add(1)
	if c.hang.Load() {
		c.gotOne <- c
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	return stream.SendMsg(&emptypb.Empty{})
}

func (c *countingCore) stop() {
	c.srv.Stop()
	<-c.done
}

func dialStatic(t *testing.T, addrs ...string) *grpc.ClientConn {
	t.Helper()
	conn, err := NewStatic(addrs, Options{Creds: insecure.NewCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func call(ctx context.Context, conn *grpc.ClientConn) error {
	return conn.Invoke(ctx, coreMethod, &emptypb.Empty{}, &emptypb.Empty{})
}

func callUntil(t *testing.T, conn *grpc.ClientConn, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for !done() {
		if ctx.Err() != nil {
			t.Fatal("condition not reached in 10s of calls")
		}
		_ = call(ctx, conn)
	}
}

func TestNewStaticRefusesBadAddressLists(t *testing.T) {
	for name, addrs := range map[string][]string{"none": nil, "empty": {""}, "repeated": {"a:1", "a:1"}} {
		if conn, err := NewStatic(addrs, Options{Creds: insecure.NewCredentials()}); err == nil {
			_ = conn.Close()
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestStaticTargetsSpreadCallsAndSkipAStoppedCore(t *testing.T) {
	a, b := startCountingCore(t, nil), startCountingCore(t, nil)
	conn := dialStatic(t, a.addr, b.addr)
	callUntil(t, conn, func() bool { return a.calls.Load() > 0 && b.calls.Load() > 0 })
	a.stop()
	callUntil(t, conn, func() bool { return call(t.Context(), conn) == nil })
	before, stopped := b.calls.Load(), a.calls.Load()
	for range 10 {
		if err := call(t.Context(), conn); err != nil {
			t.Fatalf("call after the stop = %v", err)
		}
	}
	if b.calls.Load() != before+10 || a.calls.Load() != stopped {
		t.Fatalf("calls a=%d b=%d, want all 10 on b", a.calls.Load()-stopped, b.calls.Load()-before)
	}
}

func TestCoreDyingMidCallIsUnavailableWithoutRetry(t *testing.T) {
	gotOne := make(chan *countingCore, 2)
	a, b := startCountingCore(t, gotOne), startCountingCore(t, gotOne)
	conn := dialStatic(t, a.addr, b.addr)
	callUntil(t, conn, func() bool { return a.calls.Load() > 0 && b.calls.Load() > 0 })
	a.hang.Store(true)
	b.hang.Store(true)
	before := a.calls.Load() + b.calls.Load()
	result := make(chan error, 1)
	go func() { result <- call(t.Context(), conn) }()
	(<-gotOne).stop()
	if err := <-result; status.Code(err) != codes.Unavailable {
		t.Fatalf("call = %v, want UNAVAILABLE", err)
	}
	if got := a.calls.Load() + b.calls.Load() - before; got != 1 {
		t.Fatalf("cores saw %d calls, want exactly 1", got)
	}
}
