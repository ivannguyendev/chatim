package route

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type (
	Locator = coreroute.Locator
	Dialer  = coreroute.Dialer
)

type routes interface {
	Room(roomID string) (coreroute.Conn, error)
	Any() (coreroute.Conn, error)
	Refresh(ctx context.Context) error
	Close() error
}

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

type Client struct {
	routes routes
	policy Policy
}

func New(loc Locator, dial Dialer, p Policy) (*Client, error) {
	pool, err := coreroute.NewPool(loc, dial)
	if err != nil {
		return nil, err
	}
	return newClient(pool, p)
}

func newClient(r routes, p Policy) (*Client, error) {
	if p.Deadline < 0 || p.Attempt < 0 || p.FirstDelay < 0 || p.MaxDelay < 0 {
		return nil, errors.New("route: policy durations must not be negative")
	}
	p.Deadline = cmp.Or(p.Deadline, time.Minute)
	p.Attempt = cmp.Or(p.Attempt, 5*time.Second)
	p.FirstDelay = cmp.Or(p.FirstDelay, 50*time.Millisecond)
	p.MaxDelay = cmp.Or(p.MaxDelay, time.Second)
	return &Client{routes: r, policy: p}, nil
}

func (c *Client) CreateRoom(ctx context.Context, req *chatimv1.CreateRoomRequest) (*chatimv1.CreateRoomResponse, Stats, error) {
	return anyCore(ctx, c, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.CreateRoomResponse, error) {
		return api.CreateRoom(ctx, req)
	})
}

func (c *Client) OpenDirectRoom(ctx context.Context, req *chatimv1.OpenDirectRoomRequest) (*chatimv1.OpenDirectRoomResponse, Stats, error) {
	return anyCore(ctx, c, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.OpenDirectRoomResponse, error) {
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

func (c *Client) Close() error { return c.routes.Close() }
