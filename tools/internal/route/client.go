package route

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	TenantHeader = "x-chatim-tenant"
	UserHeader   = "x-chatim-user"

	maxConns = 256
)

var errNoRoute = status.Error(codes.Unavailable, "no live core to route to")

type Locator interface {
	Addr(room uint64) (string, bool)
	AnyAddr() (string, bool)
	Refresh(ctx context.Context) error
}

type Dialer func(addr string) (chatimv1.CoreServiceClient, io.Closer, error)

type Policy struct {
	Deadline   time.Duration
	Attempt    time.Duration
	FirstDelay time.Duration
	MaxDelay   time.Duration
}

type Stats struct {
	Attempts int
	Addr     string
}

type conn struct {
	api    chatimv1.CoreServiceClient
	closer io.Closer
}

type Client struct {
	loc    Locator
	dial   Dialer
	policy Policy
	mu     sync.Mutex
	conns  map[string]conn
}

func New(loc Locator, dial Dialer, p Policy) (*Client, error) {
	switch {
	case loc == nil || dial == nil:
		return nil, errors.New("route: client needs a locator and a dialer")
	case p.Deadline < 0 || p.Attempt < 0 || p.FirstDelay < 0 || p.MaxDelay < 0:
		return nil, errors.New("route: policy durations must not be negative")
	}
	p.Deadline = cmp.Or(p.Deadline, time.Minute)
	p.Attempt = cmp.Or(p.Attempt, 5*time.Second)
	p.FirstDelay = cmp.Or(p.FirstDelay, 50*time.Millisecond)
	p.MaxDelay = cmp.Or(p.MaxDelay, time.Second)
	return &Client{loc: loc, dial: dial, policy: p, conns: map[string]conn{}}, nil
}

func WithCaller(ctx context.Context, tenant, user string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, TenantHeader, tenant, UserHeader, user)
}

func (c *Client) CreateRoom(ctx context.Context, req *chatimv1.CreateRoomRequest) (*chatimv1.CreateRoomResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryUnavailable, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.CreateRoomResponse, error) {
		return api.CreateRoom(ctx, req)
	})
}

func (c *Client) OpenDirectRoom(ctx context.Context, req *chatimv1.OpenDirectRoomRequest) (*chatimv1.OpenDirectRoomResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.OpenDirectRoomResponse, error) {
		return api.OpenDirectRoom(ctx, req)
	})
}

func (c *Client) SendMessage(ctx context.Context, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.SendMessageResponse, error) {
		return api.SendMessage(ctx, req)
	})
}

func (c *Client) GetHistory(ctx context.Context, req *chatimv1.GetHistoryRequest) (*chatimv1.GetHistoryResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetHistoryResponse, error) {
		return api.GetHistory(ctx, req)
	})
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for addr, cn := range c.conns {
		errs = append(errs, cn.closer.Close())
		delete(c.conns, addr)
	}
	return errors.Join(errs...)
}

func (c *Client) roomRoute(roomID string) (func() (string, bool), error) {
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return func() (string, bool) { return c.loc.Addr(room) }, nil
}

func (c *Client) api(addr string) (chatimv1.CoreServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cn, ok := c.conns[addr]; ok {
		return cn.api, nil
	}
	if len(c.conns) >= maxConns {
		return nil, fmt.Errorf("route: more than %d core addresses", maxConns)
	}
	api, closer, err := c.dial(addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	c.conns[addr] = conn{api: api, closer: closer}
	return api, nil
}
