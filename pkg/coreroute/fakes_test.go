package coreroute_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var quietLog = slog.New(slog.DiscardHandler)

type fakeLocator struct {
	mu        sync.Mutex
	byRoom    map[uint64]string
	any       string
	refreshes int
}

func (l *fakeLocator) Addr(room uint64) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	addr, ok := l.byRoom[room]
	return addr, ok
}

func (l *fakeLocator) AnyAddr() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.any, l.any != ""
}

func (l *fakeLocator) Refresh(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshes++
	return nil
}

type fakeAPIs struct {
	chatimv1.CoreServiceClient
	chatimv1.MentionServiceClient
	addr string
}

type coreOnly struct {
	chatimv1.CoreServiceClient
}

type fakeNet struct {
	mu        sync.Mutex
	known     map[string]bool
	dials     map[string]int
	closes    map[string]int
	closed    chan string
	noMention bool
}

func newFakeNet(addrs ...string) *fakeNet {
	n := &fakeNet{known: map[string]bool{}, dials: map[string]int{}, closes: map[string]int{}, closed: make(chan string, 1024)}
	for _, a := range addrs {
		n.known[a] = true
	}
	return n
}

func (n *fakeNet) allow(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.known[addr] = true
}

func (n *fakeNet) dial(addr string) (chatimv1.CoreServiceClient, io.Closer, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.known[addr] {
		return nil, nil, errors.New("unknown address " + addr)
	}
	n.dials[addr]++
	if n.noMention {
		return coreOnly{}, closer{n: n, addr: addr}, nil
	}
	return fakeAPIs{addr: addr}, closer{n: n, addr: addr}, nil
}

func (n *fakeNet) counts(addr string) (dials, closes int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dials[addr], n.closes[addr]
}

type closer struct {
	n    *fakeNet
	addr string
}

func (c closer) Close() error {
	c.n.mu.Lock()
	c.n.closes[c.addr]++
	c.n.mu.Unlock()
	c.n.closed <- c.addr
	return nil
}
