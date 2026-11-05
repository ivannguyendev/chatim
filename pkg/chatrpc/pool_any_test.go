package chatrpc_test

import (
	"io"
	"sync"
	"testing"

	"google.golang.org/grpc/connectivity"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type rotatingLocator struct {
	fakeLocator
	mu    sync.Mutex
	addrs []string
	next  int
}

func (l *rotatingLocator) AnyAddr() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.addrs) == 0 {
		return "", false
	}
	addr := l.addrs[l.next%len(l.addrs)]
	l.next++
	return addr, true
}

type stateNet struct {
	mu     sync.Mutex
	states map[string]connectivity.State
}

func (n *stateNet) set(addr string, s connectivity.State) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.states[addr] = s
}

func (n *stateNet) dial(addr string) (chatimv1.ChatServiceClient, io.Closer, error) {
	return fakeAPIs{addr: addr}, stateConn{n: n, addr: addr}, nil
}

type stateConn struct {
	n    *stateNet
	addr string
}

func (c stateConn) Close() error { return nil }

func (c stateConn) GetState() connectivity.State {
	c.n.mu.Lock()
	defer c.n.mu.Unlock()
	return c.n.states[c.addr]
}

func newStatePool(t *testing.T, addrs ...string) (*chatrpc.Pool, *stateNet) {
	t.Helper()
	net := &stateNet{states: map[string]connectivity.State{}}
	for _, a := range addrs {
		net.states[a] = connectivity.Ready
	}
	p, err := chatrpc.NewPool(&rotatingLocator{addrs: addrs}, net.dial)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	for range addrs {
		if _, err := p.Any(); err != nil {
			t.Fatalf("Any: %v", err)
		}
	}
	return p, net
}

func picks(t *testing.T, p *chatrpc.Pool, n int) map[string]int {
	t.Helper()
	got := map[string]int{}
	for range n {
		c, err := p.Any()
		if err != nil {
			t.Fatalf("Any: %v", err)
		}
		got[c.Addr]++
	}
	return got
}

func TestAnyRotatesOverEveryHealthyChatNode(t *testing.T) {
	p, _ := newStatePool(t, "chat-1:9000", "chat-2:9000")
	if got := picks(t, p, 4); got["chat-1:9000"] != 2 || got["chat-2:9000"] != 2 {
		t.Fatalf("picks = %v, want two each", got)
	}
}

func TestAnySkipsAChatNodeWhoseConnectionIsFailing(t *testing.T) {
	p, net := newStatePool(t, "chat-1:9000", "chat-2:9000", "chat-3:9000")
	net.set("chat-2:9000", connectivity.TransientFailure)
	got := picks(t, p, 6)
	if got["chat-2:9000"] != 0 || got["chat-1:9000"]+got["chat-3:9000"] != 6 {
		t.Fatalf("picks = %v, want chat-2 skipped", got)
	}
}

func TestAnyStillPicksIdleAndConnectingChatNodes(t *testing.T) {
	p, net := newStatePool(t, "chat-1:9000", "chat-2:9000")
	net.set("chat-1:9000", connectivity.Idle)
	net.set("chat-2:9000", connectivity.Connecting)
	if got := picks(t, p, 4); got["chat-1:9000"] != 2 || got["chat-2:9000"] != 2 {
		t.Fatalf("picks = %v, want two each", got)
	}
}

func TestAnyKeepsRotatingWhenEveryConnectionIsFailing(t *testing.T) {
	p, net := newStatePool(t, "chat-1:9000", "chat-2:9000")
	net.set("chat-1:9000", connectivity.TransientFailure)
	net.set("chat-2:9000", connectivity.Shutdown)
	if got := picks(t, p, 4); got["chat-1:9000"]+got["chat-2:9000"] != 4 {
		t.Fatalf("picks = %v, want a pick on every call", got)
	}
}
