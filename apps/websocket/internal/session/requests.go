package session

import (
	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	messageRate     = "too many requests"
	messageInflight = "too many requests in flight"
	messageShutdown = "server shutting down, retry later"
	messageInternal = "internal error"
	messageAuth     = "authentication required"

	wireResourceExhausted = int32(codes.ResourceExhausted)
	wireUnavailable       = int32(codes.Unavailable)
	wireInternal          = int32(codes.Internal)
	wireUnauthenticated   = int32(codes.Unauthenticated)
)

var sweepingCodes = map[codes.Code]bool{codes.Unavailable: true, codes.DeadlineExceeded: true, codes.Canceled: true}

type clientCommand struct {
	req    *chatimv1.ClientRequest
	claims jwtverify.Claims
}

type roomQueue struct {
	waiting []clientCommand
}

func (c *connection) request(req *chatimv1.ClientRequest, allowed bool, claims jwtverify.Claims, running bool) {
	switch {
	case !allowed:
		c.reject(req, rejectRate, wireResourceExhausted, messageRate)
	case !running:
		c.respond(&chatimv1.ServerResponse{Id: req.GetId(), Code: wireUnauthenticated, Message: messageAuth})
	case !c.server.commands.enter():
		c.reject(req, rejectShutdown, wireUnavailable, messageShutdown)
	case c.inflight.Add(1) > int64(c.server.opts.CommandInflight):
		c.release()
		c.reject(req, rejectInflight, wireResourceExhausted, messageInflight)
	default:
		c.dispatch(clientCommand{req: req, claims: claims})
	}
}

func (c *connection) reject(req *chatimv1.ClientRequest, reason rejectReason, code int32, message string) {
	c.server.stats.reject(reason)
	c.respond(&chatimv1.ServerResponse{Id: req.GetId(), Code: code, Message: message})
}

func (c *connection) release() {
	c.inflight.Add(-1)
	c.server.commands.leave()
}

func (c *connection) dispatch(cmd clientCommand) {
	if room, ok := orderedRoom(cmd.req); ok {
		c.enqueueSend(room, cmd)
		return
	}
	c.work.Go(func() { c.guarded(cmd) })
}

func orderedRoom(req *chatimv1.ClientRequest) (uint64, bool) {
	var roomID string
	switch r := req.GetRequest().(type) {
	case *chatimv1.ClientRequest_SendMessage:
		roomID = r.SendMessage.GetRoomId()
	case *chatimv1.ClientRequest_MarkRead:
		roomID = r.MarkRead.GetRoomId()
	case *chatimv1.ClientRequest_MarkUnread:
		roomID = r.MarkUnread.GetRoomId()
	default:
		return 0, false
	}
	room, err := ids.ParseRoomID(roomID)
	return room, err == nil
}

func (c *connection) guarded(cmd clientCommand) (resp *chatimv1.ServerResponse, code codes.Code, ok bool) {
	defer c.release()
	defer func() {
		if r := recover(); r != nil {
			c.panicked(r)
			ok = false
		}
	}()
	req := cmd.req
	resp, outcome := c.server.executor.Execute(c.ctx, cmd.claims, req)
	if resp == nil {
		c.server.logger.Error("websocket command returned no response", "command", outcome.Command)
		resp = &chatimv1.ServerResponse{Id: req.GetId(), Code: wireInternal, Message: messageInternal}
		outcome.Code = codes.Internal
	}
	if outcome.Code == codes.Unimplemented && outcome.Command == "" {
		c.server.stats.reject(rejectUnimplemented)
	}
	c.respond(resp)
	return resp, outcome.Code, true
}

func (c *connection) enqueueSend(room uint64, cmd clientCommand) {
	c.mu.Lock()
	if q, ok := c.rooms[room]; ok {
		q.waiting = append(q.waiting, cmd)
		c.mu.Unlock()
		return
	}
	q := &roomQueue{}
	c.rooms[room] = q
	c.mu.Unlock()
	c.work.Go(func() { c.runRoom(room, q, cmd) })
}

func (c *connection) runRoom(room uint64, q *roomQueue, cmd clientCommand) {
	defer func() {
		if r := recover(); r != nil {
			c.panicked(r)
			c.dropRoom(room, q)
		}
	}()
	for more := true; more; cmd, more = c.next(room, q) {
		if c.ctx.Err() != nil {
			c.release()
			c.dropRoom(room, q)
			return
		}
		resp, code, ok := c.guarded(cmd)
		if !ok {
			c.dropRoom(room, q)
			return
		}
		if sweepingCodes[code] && cmd.req.GetSendMessage() != nil {
			c.sweep(q, resp)
		}
	}
}

func (c *connection) sweep(q *roomQueue, failed *chatimv1.ServerResponse) {
	c.mu.Lock()
	queued := len(q.waiting)
	c.mu.Unlock()
	for range queued {
		c.answerSwept(c.pop(q).req, failed)
	}
}

func (c *connection) answerSwept(req *chatimv1.ClientRequest, failed *chatimv1.ServerResponse) {
	defer c.release()
	c.respond(&chatimv1.ServerResponse{Id: req.GetId(), Code: failed.GetCode(), Message: failed.GetMessage()})
}

func (c *connection) pop(q *roomQueue) clientCommand {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := q.waiting[0]
	q.waiting[0] = clientCommand{}
	q.waiting = q.waiting[1:]
	return cmd
}

func (c *connection) next(room uint64, q *roomQueue) (clientCommand, bool) {
	c.mu.Lock()
	empty := len(q.waiting) == 0
	if empty {
		delete(c.rooms, room)
	}
	c.mu.Unlock()
	if empty {
		return clientCommand{}, false
	}
	return c.pop(q), true
}

func (c *connection) dropRoom(room uint64, q *roomQueue) {
	c.mu.Lock()
	dropped := q.waiting
	q.waiting = nil
	if c.rooms[room] == q {
		delete(c.rooms, room)
	}
	c.mu.Unlock()
	for range dropped {
		c.release()
	}
}
