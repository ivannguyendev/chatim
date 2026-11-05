package chatrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const MaxAddresses = slotmap.MaxNodes

type Locator interface {
	Addr(room uint64) (string, bool)
	AnyAddr() (string, bool)
	Refresh(ctx context.Context) error
}

type Dialer func(addr string) (chatimv1.ChatServiceClient, io.Closer, error)

type PoolConn struct {
	Addr string
	Clients
}

type pooled struct {
	conn   PoolConn
	closer io.Closer
}

type Pool struct {
	loc    Locator
	dial   Dialer
	mu     sync.Mutex
	conns  map[string]pooled
	closed bool
}

func NewPool(loc Locator, dial Dialer) (*Pool, error) {
	if loc == nil || dial == nil {
		return nil, errors.New("chatrpc: pool needs a locator and a dialer")
	}
	return &Pool{loc: loc, dial: dial, conns: map[string]pooled{}}, nil
}

func ParseRoom(roomID string) (uint64, error) {
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		return 0, status.Error(codes.InvalidArgument, err.Error())
	}
	return room, nil
}

func (p *Pool) Room(roomID string) (PoolConn, error) {
	room, err := ParseRoom(roomID)
	if err != nil {
		return PoolConn{}, err
	}
	addr, ok := p.loc.Addr(room)
	return p.at(addr, ok)
}

func (p *Pool) Any() (PoolConn, error) {
	first, ok := p.loc.AnyAddr()
	if !ok {
		return PoolConn{}, ErrNoRoute
	}
	addr := first
	for range p.size() {
		if !p.failing(addr) {
			return p.at(addr, true)
		}
		next, ok := p.loc.AnyAddr()
		if !ok || next == first {
			break
		}
		addr = next
	}
	if p.failing(addr) {
		addr = first
	}
	return p.at(addr, true)
}

type stateReporter interface {
	GetState() connectivity.State
}

func (p *Pool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

func (p *Pool) failing(addr string) bool {
	p.mu.Lock()
	pc, found := p.conns[addr]
	p.mu.Unlock()
	if !found {
		return false
	}
	r, ok := pc.closer.(stateReporter)
	if !ok {
		return false
	}
	s := r.GetState()
	return s == connectivity.TransientFailure || s == connectivity.Shutdown
}

func (p *Pool) Refresh(ctx context.Context) error { return p.loc.Refresh(ctx) }

func (p *Pool) Prune(live []string) {
	keep := make(map[string]bool, len(live))
	for _, addr := range live {
		keep[addr] = true
	}
	p.mu.Lock()
	var gone []io.Closer
	for addr, pc := range p.conns {
		if !keep[addr] {
			gone = append(gone, pc.closer)
			delete(p.conns, addr)
		}
	}
	p.mu.Unlock()
	for _, c := range gone {
		_ = c.Close()
	}
}

func (p *Pool) Close() error {
	p.mu.Lock()
	p.closed = true
	conns := p.conns
	p.conns = map[string]pooled{}
	p.mu.Unlock()
	var errs []error
	for _, pc := range conns {
		errs = append(errs, pc.closer.Close())
	}
	return errors.Join(errs...)
}

func (p *Pool) at(addr string, ok bool) (PoolConn, error) {
	if !ok {
		return PoolConn{}, ErrNoRoute
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch pc, found := p.conns[addr]; {
	case p.closed:
		return PoolConn{Addr: addr}, errClosed
	case found:
		return pc.conn, nil
	case len(p.conns) >= MaxAddresses:
		return PoolConn{Addr: addr}, unavailable(ErrTooManyAddresses)
	}
	chat, closer, err := p.dial(addr)
	if err != nil {
		return PoolConn{Addr: addr}, unavailable(fmt.Errorf("%w %s: %w", ErrDial, addr, err))
	}
	mention, _ := chat.(chatimv1.MentionServiceClient)
	c := PoolConn{Addr: addr, Clients: Clients{Chat: chat, Mention: mention}}
	p.conns[addr] = pooled{conn: c, closer: closer}
	return c, nil
}
