package wsclient

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	Subprotocol        = "chatim.v1"
	outboxSize         = 64
	closeTimeout       = time.Second
	handlerWaitTimeout = 5 * time.Second
)

type Client struct {
	conn       *gws.Conn
	out        chan []byte
	done       chan struct{}
	closing    chan struct{}
	closeOnce  sync.Once
	dispatched chan struct{}
	nextID     atomic.Uint64
	loops      sync.WaitGroup
	mu         sync.Mutex
	pending    map[uint64]chan *chatimv1.ServerResponse
	err        error
	opts       options
	inbox      chan *chatimv1.ServerFrame
	backlog    chan struct{}
	loadTimer  *time.Timer
	reads      readThrottle
	readers    sync.WaitGroup
}

func Dial(ctx context.Context, url, token string, opts ...Option) (*Client, error) {
	option := clientOption(ctx, url)
	option.NewDialer = func() (gws.Dialer, error) { return contextDialer{ctx: ctx}, nil }
	return connect(token, opts, func(events gws.Event) (*gws.Conn, *http.Response, error) {
		return gws.NewClient(events, option)
	})
}

func DialConn(ctx context.Context, conn net.Conn, url, token string, opts ...Option) (*Client, error) {
	return connect(token, opts, func(events gws.Event) (*gws.Conn, *http.Response, error) {
		return gws.NewClientFromConn(events, clientOption(ctx, url), conn)
	})
}

func (c *Client) Call(ctx context.Context, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
	if listsRooms(req) {
		if err := c.flushReads(ctx); err != nil {
			return nil, err
		}
	}
	id, reply, err := c.start(ctx, req)
	if err != nil {
		return nil, err
	}
	return c.wait(ctx, id, reply)
}

func (c *Client) start(ctx context.Context, req *chatimv1.ClientRequest) (uint64, chan *chatimv1.ServerResponse, error) {
	id := c.nextID.Add(1)
	frame, err := proto.Marshal(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Request{
		Request: &chatimv1.ClientRequest{Id: id, Request: req.GetRequest()},
	}})
	if err != nil {
		return 0, nil, err
	}
	reply := make(chan *chatimv1.ServerResponse, 1)
	if err := c.register(id, reply); err != nil {
		return 0, nil, err
	}
	if err := c.send(ctx, frame); err != nil {
		c.forget(id)
		return 0, nil, err
	}
	return id, reply, nil
}

func (c *Client) wait(ctx context.Context, id uint64, reply chan *chatimv1.ServerResponse) (*chatimv1.ServerResponse, error) {
	select {
	case resp := <-reply:
		return resp, responseError(resp)
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	case <-c.done:
		select {
		case resp := <-reply:
			return resp, responseError(resp)
		default:
			return nil, c.Err()
		}
	}
}

func (c *Client) Renew(token string) error {
	return c.send(context.Background(), authFrame(token))
}

func (c *Client) CloseAsync() {
	c.closeOnce.Do(func() { close(c.closing) })
}

func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return c.CloseContext(ctx)
}

func (c *Client) CloseContext(ctx context.Context) error {
	flushErr := c.flushReads(ctx)
	c.CloseAsync()
	<-c.done
	c.loops.Wait()
	c.stopReads()
	timer := time.NewTimer(handlerWaitTimeout)
	defer timer.Stop()
	select {
	case <-c.dispatched:
	case <-timer.C:
		return ErrHandlerStillRunning
	}
	if ctx.Err() != nil {
		return flushErr
	}
	return nil
}

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func connect(token string, opts []Option, open func(gws.Event) (*gws.Conn, *http.Response, error)) (*Client, error) {
	o := buildOptions(opts)
	c := &Client{
		out:        make(chan []byte, outboxSize),
		done:       make(chan struct{}),
		closing:    make(chan struct{}),
		dispatched: make(chan struct{}),
		pending:    make(map[uint64]chan *chatimv1.ServerResponse),
		opts:       o,
		inbox:      make(chan *chatimv1.ServerFrame, o.dispatchQueue),
		backlog:    make(chan struct{}, 1),
		reads:      newReadThrottle(),
	}
	conn, resp, err := open(events{client: c})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.out <- authFrame(token)
	c.loops.Add(2)
	go func() {
		defer c.loops.Done()
		conn.ReadLoop()
	}()
	go func() {
		defer c.loops.Done()
		c.writeLoop()
	}()
	go c.dispatchLoop()
	return c, nil
}

func clientOption(ctx context.Context, url string) *gws.ClientOption {
	option := &gws.ClientOption{
		Addr:          url,
		RequestHeader: http.Header{"Sec-Websocket-Protocol": []string{Subprotocol}},
	}
	if deadline, ok := ctx.Deadline(); ok {
		option.HandshakeTimeout = time.Until(deadline)
	}
	return option
}

func (c *Client) send(ctx context.Context, frame []byte) error {
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	select {
	case c.out <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
}

func (c *Client) register(id uint64, reply chan *chatimv1.ServerResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return c.err
	}
	c.pending[id] = reply
	return nil
}

func (c *Client) forget(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

func (c *Client) deliver(resp *chatimv1.ServerResponse) {
	c.mu.Lock()
	reply, ok := c.pending[resp.GetId()]
	delete(c.pending, resp.GetId())
	c.mu.Unlock()
	if ok {
		reply <- resp
	}
}

func (c *Client) finish(err error) {
	c.mu.Lock()
	c.err = err
	c.pending = nil
	c.mu.Unlock()
	close(c.done)
}

func responseError(resp *chatimv1.ServerResponse) error {
	code := resp.GetCode()
	switch {
	case code == 0:
		return nil
	case code < 0:
		return status.Error(codes.Unknown, resp.GetMessage())
	default:
		return status.Error(codes.Code(code), resp.GetMessage())
	}
}

func authFrame(token string) []byte {
	frame, _ := proto.Marshal(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Auth{
		Auth: &chatimv1.AuthFrame{Token: token},
	}})
	return frame
}

type contextDialer struct{ ctx context.Context }

func (d contextDialer) Dial(network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(d.ctx, network, address)
}
