package chatrpc_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func newPool(t *testing.T, loc chatrpc.Locator, net *fakeNet) *chatrpc.Pool {
	t.Helper()
	p, err := chatrpc.NewPool(loc, net.dial)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestPoolPicksTheChatOfTheRoomSlotAndReusesItsConnection(t *testing.T) {
	net := newFakeNet("chat-1:9000", "chat-2:9000")
	loc := &fakeLocator{byRoom: map[uint64]string{42: "chat-2:9000", 7: "chat-1:9000"}, any: "chat-1:9000"}
	p := newPool(t, loc, net)
	for range 3 {
		c, err := p.Room("42")
		if err != nil || c.Addr != "chat-2:9000" || c.Chat == nil || c.Mention == nil {
			t.Fatalf("Room(42) = %+v, %v; want chat-2 with both services", c, err)
		}
	}
	if c, err := p.Room("7"); err != nil || c.Addr != "chat-1:9000" {
		t.Fatalf("Room(7) = %+v, %v; want chat-1", c, err)
	}
	if c, err := p.Any(); err != nil || c.Addr != "chat-1:9000" {
		t.Fatalf("Any = %+v, %v; want chat-1", c, err)
	}
	if d, _ := net.counts("chat-2:9000"); d != 1 {
		t.Fatalf("chat-2 dialed %d times, want once", d)
	}
	if d, _ := net.counts("chat-1:9000"); d != 1 {
		t.Fatalf("chat-1 dialed %d times, want once", d)
	}
}

func TestPoolRejectsABadRoomAndReportsNoRoute(t *testing.T) {
	p := newPool(t, &fakeLocator{}, newFakeNet())
	if _, err := p.Room("not-a-room"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Room(bad) = %v, want InvalidArgument", err)
	}
	if _, err := chatrpc.ParseRoom("0"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ParseRoom(0) = %v, want InvalidArgument", err)
	}
	if room, err := chatrpc.ParseRoom("42"); err != nil || room != 42 {
		t.Fatalf("ParseRoom(42) = %d, %v", room, err)
	}
	_, err := p.Room("42")
	if s, _ := status.FromError(err); s.Code() != codes.Unavailable || s.Message() != "no live chat to route to" {
		t.Fatalf("Room without a chat = %v, want Unavailable no live chat", err)
	}
	if _, err := p.Any(); !errors.Is(err, chatrpc.ErrNoRoute) {
		t.Fatalf("Any without a chat = %v, want ErrNoRoute", err)
	}
}

func TestPoolReturnsUnavailableStatusesForDialFailuresAndTheAddressCap(t *testing.T) {
	net := newFakeNet()
	loc := &fakeLocator{any: "chat-x:9000"}
	p := newPool(t, loc, net)
	c, err := p.Any()
	if _, ok := status.FromError(err); !ok || status.Code(err) != codes.Unavailable || !errors.Is(err, chatrpc.ErrDial) || c.Addr != "chat-x:9000" {
		t.Fatalf("Any to an undialable chat = %+v, %v; want an Unavailable status wrapping ErrDial", c, err)
	}
	for i := range chatrpc.MaxAddresses {
		addr := fmt.Sprintf("chat-%d:9000", i)
		net.allow(addr)
		loc.mu.Lock()
		loc.any = addr
		loc.mu.Unlock()
		if _, err := p.Any(); err != nil {
			t.Fatalf("Any to address %d: %v", i, err)
		}
	}
	net.allow("chat-over:9000")
	loc.mu.Lock()
	loc.any = "chat-over:9000"
	loc.mu.Unlock()
	_, err = p.Any()
	if _, ok := status.FromError(err); !ok || status.Code(err) != codes.Unavailable || !errors.Is(err, chatrpc.ErrTooManyAddresses) {
		t.Fatalf("Any over the cap = %v, want an Unavailable status wrapping ErrTooManyAddresses", err)
	}
}

func TestPoolPrunesConnectionsToAddressesThatLeftTheTable(t *testing.T) {
	net := newFakeNet("chat-1:9000", "chat-2:9000")
	loc := &fakeLocator{byRoom: map[uint64]string{42: "chat-1:9000"}, any: "chat-2:9000"}
	p := newPool(t, loc, net)
	if _, err := p.Room("42"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Any(); err != nil {
		t.Fatal(err)
	}
	p.Prune([]string{"chat-2:9000"})
	if _, closes := net.counts("chat-1:9000"); closes != 1 {
		t.Fatalf("chat-1 closed %d times after leaving the table, want once", closes)
	}
	if _, closes := net.counts("chat-2:9000"); closes != 0 {
		t.Fatalf("chat-2 closed %d times while still in the table", closes)
	}
	if _, err := p.Room("42"); err != nil {
		t.Fatal(err)
	}
	if d, _ := net.counts("chat-1:9000"); d != 2 {
		t.Fatalf("chat-1 dialed %d times, want a fresh dial after the prune", d)
	}
}

func TestPoolLeavesTheMentionClientEmptyWhenTheDialerHasNone(t *testing.T) {
	net := newFakeNet("chat-1:9000")
	net.noMention = true
	p := newPool(t, &fakeLocator{any: "chat-1:9000"}, net)
	if c, err := p.Any(); err != nil || c.Chat == nil || c.Mention != nil {
		t.Fatalf("Any = %+v, %v; want a chat client and no mention client", c, err)
	}
}

func TestPoolCloseIsIdempotentAndRefusesLaterCalls(t *testing.T) {
	net := newFakeNet("chat-1:9000")
	p, err := chatrpc.NewPool(&fakeLocator{any: "chat-1:9000"}, net.dial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Any(); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(p.Close(), p.Close()); err != nil {
		t.Fatal(err)
	}
	if _, closes := net.counts("chat-1:9000"); closes != 1 {
		t.Fatalf("chat-1 closed %d times, want once", closes)
	}
	if _, err := p.Any(); status.Code(err) != codes.Unavailable {
		t.Fatalf("Any after Close = %v, want Unavailable", err)
	}
	if _, err := chatrpc.NewPool(nil, net.dial); err == nil {
		t.Fatal("NewPool accepted a nil locator")
	}
	if _, err := chatrpc.NewPool(&fakeLocator{}, nil); err == nil {
		t.Fatal("NewPool accepted a nil dialer")
	}
	if chatrpc.MaxAddresses != slotmap.MaxNodes {
		t.Fatalf("MaxAddresses = %d, want the resolver chat cap %d", chatrpc.MaxAddresses, slotmap.MaxNodes)
	}
}
