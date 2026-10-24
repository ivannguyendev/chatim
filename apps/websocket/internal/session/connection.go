package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const sessionKey = "chatim.connection"

var decodeOptions = proto.UnmarshalOptions{DiscardUnknown: true}

type frameKind int

const (
	frameUnknown frameKind = iota
	frameAuth
	frameHeartbeat
	frameRequest
	frameOpenRoom
	frameCloseRoom
	frameLoadRooms
	frameTyping
)

type connection struct {
	server   *Server
	socket   *gws.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	limiter  *rate.Limiter
	typist   *rate.Limiter
	out      chan outFrame
	closeReq chan uint16
	closed   chan struct{}
	closing  atomic.Uint32
	aborted  atomic.Bool
	inflight atomic.Int64
	work     sync.WaitGroup

	queueMu     sync.Mutex
	queuedBytes int64
	sealed      bool

	mu        sync.Mutex
	authed    bool
	finished  bool
	claims    jwtverify.Claims
	authTimer *time.Timer
	expiry    *time.Timer
	rooms     map[uint64]*roomQueue
	member    *interest.Conn
	closeErr  error
}

func newConnection(s *Server, socket *gws.Conn) *connection {
	ctx, cancel := context.WithCancel(context.Background())
	return &connection{
		server:   s,
		socket:   socket,
		ctx:      ctx,
		cancel:   cancel,
		limiter:  rate.NewLimiter(rate.Limit(s.opts.CommandRate), s.opts.CommandBurst),
		typist:   rate.NewLimiter(rate.Limit(s.opts.TypingRate), s.opts.TypingBurst),
		out:      make(chan outFrame, s.opts.SendQueue),
		closeReq: make(chan uint16, 1),
		closed:   make(chan struct{}),
		rooms:    map[uint64]*roomQueue{},
	}
}

func (c *connection) run() {
	c.socket.Session().Store(sessionKey, c)
	c.work.Go(c.writeLoop)
	c.mu.Lock()
	c.authTimer = time.AfterFunc(c.server.opts.AuthTimeout, c.authTimedOut)
	c.mu.Unlock()
	_ = c.socket.SetReadDeadline(time.Now().Add(c.server.opts.AuthTimeout + authReadGrace))
	c.socket.ReadLoop()
	c.work.Wait()
	c.sealQueue()
	if code, ok := c.closeCodeOf(c.closeErr); ok {
		c.server.stats.closed(code)
	}
}

func (c *connection) receive(message *gws.Message) {
	defer c.recoverPanic()
	if c.closing.Load() != 0 {
		return
	}
	if message.Opcode != gws.OpcodeBinary {
		c.closeWith(CodeUnsupportedData)
		return
	}
	var frame chatimv1.ClientFrame
	if err := decodeOptions.Unmarshal(message.Bytes(), &frame); err != nil {
		c.closeWith(CodePolicyViolation)
		return
	}
	kind := kindOf(&frame)
	allowed := c.limiter.Allow()
	switch {
	case kind != frameAuth && !c.isAuthed():
		c.closeWith(CodePolicyViolation)
	case kind == frameRequest:
		c.request(frame.GetRequest(), allowed)
	case !allowed:
		c.closeWith(CodePolicyViolation)
	case kind == frameAuth:
		c.authenticate(frame.GetAuth().GetToken())
	case kind == frameOpenRoom:
		c.openRoom(frame.GetOpenRoom().GetRoomId())
	case kind == frameCloseRoom:
		c.closeRoom(frame.GetCloseRoom().GetRoomId())
	case kind == frameLoadRooms:
		c.loadRooms()
	case kind == frameTyping:
		c.typing(frame.GetTyping())
	case kind == frameUnknown:
		c.server.stats.unknownFrames.Add(1)
	}
	c.renewLiveness()
}

func (c *connection) control(payload []byte, reply bool) {
	if c.closing.Load() != 0 {
		return
	}
	if !c.limiter.Allow() {
		c.closeWith(CodePolicyViolation)
		return
	}
	if reply {
		c.enqueue(outFrame{opcode: gws.OpcodePong, payload: payload})
	}
	c.renewLiveness()
}

func (c *connection) renewLiveness() {
	claims, authed := c.authedClaims()
	if !authed {
		return
	}
	now := time.Now()
	_ = c.socket.SetReadDeadline(now.Add(c.server.opts.HeartbeatTimeout))
	c.server.presence.Touch(claims.Tenant, claims.User, now)
}

func kindOf(frame *chatimv1.ClientFrame) frameKind {
	switch frame.GetFrame().(type) {
	case *chatimv1.ClientFrame_Auth:
		return frameAuth
	case *chatimv1.ClientFrame_Heartbeat:
		return frameHeartbeat
	case *chatimv1.ClientFrame_Request:
		return frameRequest
	case *chatimv1.ClientFrame_OpenRoom:
		return frameOpenRoom
	case *chatimv1.ClientFrame_CloseRoom:
		return frameCloseRoom
	case *chatimv1.ClientFrame_LoadRooms:
		return frameLoadRooms
	case *chatimv1.ClientFrame_Typing:
		return frameTyping
	default:
		return frameUnknown
	}
}

func (c *connection) authedClaims() (jwtverify.Claims, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claims, c.authed
}

func (c *connection) isAuthed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authed
}

func (c *connection) finish(err error) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	c.closeErr = err
	authed := c.authed
	member := c.member
	c.member = nil
	stopTimer(c.authTimer)
	stopTimer(c.expiry)
	c.mu.Unlock()
	if !authed {
		c.server.stats.unauth.Add(-1)
	}
	if member != nil {
		member.Unregister()
	}
	c.cancel()
	close(c.closed)
}

type statusCode interface {
	error
	Uint16() uint16
}

func (c *connection) closeCodeOf(err error) (uint16, bool) {
	if c.aborted.Load() {
		return CodeAbnormal, true
	}
	if code, ok := errors.AsType[statusCode](err); ok {
		return code.Uint16(), true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return CodeNormal, true
	}
	return 0, false
}

func (c *connection) recoverPanic() {
	if r := recover(); r != nil {
		c.panicked(r)
	}
}

func (c *connection) recoverWriter() {
	if r := recover(); r != nil {
		c.server.logger.Error("websocket writer panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		c.closing.CompareAndSwap(0, uint32(CodeInternalError))
		c.abort()
	}
}

func (c *connection) panicked(r any) {
	c.server.logger.Error("websocket connection panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	c.closeWith(CodeInternalError)
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

type events struct {
	gws.BuiltinEventHandler
}

func (events) OnPing(socket *gws.Conn, payload []byte) {
	if c := connectionOf(socket); c != nil {
		c.control(payload, true)
	}
}

func (events) OnPong(socket *gws.Conn, payload []byte) {
	if c := connectionOf(socket); c != nil {
		c.control(payload, false)
	}
}

func (events) OnMessage(socket *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	if c := connectionOf(socket); c != nil {
		c.receive(message)
	}
}

func (events) OnClose(socket *gws.Conn, err error) {
	if c := connectionOf(socket); c != nil {
		c.finish(err)
	}
}

func connectionOf(socket *gws.Conn) *connection {
	v, ok := socket.Session().Load(sessionKey)
	if !ok {
		return nil
	}
	c, _ := v.(*connection)
	return c
}
