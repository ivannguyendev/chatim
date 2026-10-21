package coreroute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const MaxAddresses = slotmap.MaxCores

type Locator interface {
	Addr(room uint64) (string, bool)
	AnyAddr() (string, bool)
	Refresh(ctx context.Context) error
}

type Dialer func(addr string) (chatimv1.CoreServiceClient, io.Closer, error)

type Conn struct {
	Addr    string
	Core    chatimv1.CoreServiceClient
	Mention chatimv1.MentionServiceClient
}

type pooled struct {
	conn   Conn
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
		return nil, errors.New("coreroute: pool needs a locator and a dialer")
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

func (p *Pool) Room(roomID string) (Conn, error) {
	room, err := ParseRoom(roomID)
	if err != nil {
		return Conn{}, err
	}
	addr, ok := p.loc.Addr(room)
	return p.at(addr, ok)
}

func (p *Pool) Any() (Conn, error) {
	addr, ok := p.loc.AnyAddr()
	return p.at(addr, ok)
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

func (p *Pool) at(addr string, ok bool) (Conn, error) {
	if !ok {
		return Conn{}, ErrNoRoute
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch pc, found := p.conns[addr]; {
	case p.closed:
		return Conn{Addr: addr}, errClosed
	case found:
		return pc.conn, nil
	case len(p.conns) >= MaxAddresses:
		return Conn{Addr: addr}, unavailable(ErrTooManyAddresses)
	}
	core, closer, err := p.dial(addr)
	if err != nil {
		return Conn{Addr: addr}, unavailable(fmt.Errorf("%w %s: %w", ErrDial, addr, err))
	}
	mention, _ := core.(chatimv1.MentionServiceClient)
	c := Conn{Addr: addr, Core: core, Mention: mention}
	p.conns[addr] = pooled{conn: c, closer: closer}
	return c, nil
}
