package wstest

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
	Subprotocol       = "chatim.v1"
	HeartbeatInterval = 5 * time.Second

	writeTimeout = 10 * time.Second
	closeTimeout = time.Second
	closeNormal  = 1000
)

type Conn struct {
	conn      *gws.Conn
	writeMu   sync.Mutex
	nextID    atomic.Uint64
	mu        sync.Mutex
	pending   map[uint64]chan *chatimv1.ServerResponse
	err       error
	done      chan struct{}
	closing   chan struct{}
	closeOnce sync.Once
	queue     *frameQueue
	loops     sync.WaitGroup
}

func Dial(ctx context.Context, url string) (*Conn, error) {
	option := clientOption(ctx, url)
	option.NewDialer = func() (gws.Dialer, error) { return contextDialer{ctx: ctx}, nil }
	return open(func(events gws.Event) (*gws.Conn, *http.Response, error) {
		return gws.NewClient(events, option)
	})
}

func dialConn(ctx context.Context, conn net.Conn, url string) (*Conn, error) {
	return open(func(events gws.Event) (*gws.Conn, *http.Response, error) {
		return gws.NewClientFromConn(events, clientOption(ctx, url), conn)
	})
}

func open(upgrade func(gws.Event) (*gws.Conn, *http.Response, error)) (*Conn, error) {
	c := &Conn{
		pending: make(map[uint64]chan *chatimv1.ServerResponse),
		done:    make(chan struct{}),
		closing: make(chan struct{}),
		queue:   newFrameQueue(),
	}
	conn, resp, err := upgrade(events{c: c})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.loops.Go(conn.ReadLoop)
	c.loops.Go(c.heartbeat)
	c.loops.Go(func() { c.queue.run(c.closing) })
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

func (c *Conn) Auth(token string) error {
	return c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Auth{Auth: &chatimv1.AuthFrame{Token: token}}})
}

func (c *Conn) Call(ctx context.Context, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
	id := c.nextID.Add(1)
	reply := make(chan *chatimv1.ServerResponse, 1)
	if err := c.register(id, reply); err != nil {
		return nil, err
	}
	frame := &chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Request{Request: &chatimv1.ClientRequest{Id: id, Request: req.GetRequest()}}}
	if err := c.Send(frame); err != nil {
		c.forget(id)
		return nil, err
	}
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

func (c *Conn) Send(frame *chatimv1.ClientFrame) error {
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return c.SendRaw(gws.OpcodeBinary, payload)
}

func (c *Conn) SendRaw(opcode gws.Opcode, payload []byte) error {
	select {
	case <-c.done:
		return c.Err()
	case <-c.closing:
		return ErrClosed
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.NetConn().SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteMessage(opcode, payload)
}

func (c *Conn) Frames() <-chan *chatimv1.ServerFrame { return c.queue.out }

func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return c.CloseContext(ctx)
}

func (c *Conn) CloseContext(ctx context.Context) error {
	c.closeOnce.Do(func() {
		close(c.closing)
		c.writeMu.Lock()
		_ = c.conn.NetConn().SetWriteDeadline(time.Now().Add(closeTimeout))
		_ = c.conn.WriteClose(closeNormal, nil)
		c.writeMu.Unlock()
	})
	var err error
	select {
	case <-c.done:
	case <-ctx.Done():
		_ = c.conn.NetConn().Close()
		<-c.done
		err = ctx.Err()
	}
	c.loops.Wait()
	return err
}

func (c *Conn) heartbeat() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Heartbeat{Heartbeat: &chatimv1.HeartbeatFrame{}}})
		case <-c.closing:
			return
		case <-c.done:
			return
		}
	}
}

func (c *Conn) register(id uint64, reply chan *chatimv1.ServerResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return c.err
	}
	c.pending[id] = reply
	return nil
}

func (c *Conn) forget(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

func (c *Conn) deliver(resp *chatimv1.ServerResponse) {
	c.mu.Lock()
	reply, ok := c.pending[resp.GetId()]
	delete(c.pending, resp.GetId())
	c.mu.Unlock()
	if ok {
		reply <- resp
	}
}

func (c *Conn) finish(err error) {
	c.mu.Lock()
	c.err = err
	c.pending = nil
	c.mu.Unlock()
	c.queue.end()
	close(c.done)
}

func responseError(resp *chatimv1.ServerResponse) error {
	switch code := resp.GetCode(); {
	case code == 0:
		return nil
	case code < 0:
		return status.Error(codes.Unknown, resp.GetMessage())
	default:
		return status.Error(codes.Code(code), resp.GetMessage())
	}
}

type contextDialer struct{ ctx context.Context }

func (d contextDialer) Dial(network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(d.ctx, network, address)
}
