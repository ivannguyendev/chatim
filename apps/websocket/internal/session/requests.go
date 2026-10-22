package session

import (
	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	messageRate     = "too many requests"
	messageInflight = "too many requests in flight"
	messageShutdown = "server shutting down, retry later"
	messageInternal = "internal error"

	wireResourceExhausted = int32(codes.ResourceExhausted)
	wireUnavailable       = int32(codes.Unavailable)
	wireInternal          = int32(codes.Internal)
)

var sweepingCodes = map[codes.Code]bool{codes.Unavailable: true, codes.DeadlineExceeded: true, codes.Canceled: true}

type roomQueue struct {
	waiting []*chatimv1.ClientRequest
}

func (c *connection) request(req *chatimv1.ClientRequest, allowed bool) {
	switch {
	case !allowed:
		c.reject(req, rejectRate, wireResourceExhausted, messageRate)
	case !c.server.commands.enter():
		c.reject(req, rejectShutdown, wireUnavailable, messageShutdown)
	case c.inflight.Add(1) > int64(c.server.opts.CommandInflight):
		c.release()
		c.reject(req, rejectInflight, wireResourceExhausted, messageInflight)
	default:
		c.dispatch(req)
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

func (c *connection) dispatch(req *chatimv1.ClientRequest) {
	if room, ok := orderedRoom(req); ok {
		c.enqueueSend(room, req)
		return
	}
	c.work.Go(func() { c.guarded(req) })
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

func (c *connection) guarded(req *chatimv1.ClientRequest) (resp *chatimv1.ServerResponse, code codes.Code, ok bool) {
	defer c.release()
	defer func() {
		if r := recover(); r != nil {
			c.panicked(r)
			ok = false
		}
	}()
	resp, outcome := c.server.executor.Execute(c.ctx, c.currentClaims(), req)
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

func (c *connection) enqueueSend(room uint64, req *chatimv1.ClientRequest) {
	c.mu.Lock()
	if q, ok := c.rooms[room]; ok {
		q.waiting = append(q.waiting, req)
		c.mu.Unlock()
		return
	}
	q := &roomQueue{}
	c.rooms[room] = q
	c.mu.Unlock()
	c.work.Go(func() { c.runRoom(room, q, req) })
}

func (c *connection) runRoom(room uint64, q *roomQueue, req *chatimv1.ClientRequest) {
	defer func() {
		if r := recover(); r != nil {
			c.panicked(r)
			c.dropRoom(room, q)
		}
	}()
	for req != nil {
		if c.ctx.Err() != nil {
			c.release()
			c.dropRoom(room, q)
			return
		}
		resp, code, ok := c.guarded(req)
		if !ok {
			c.dropRoom(room, q)
			return
		}
		if sweepingCodes[code] {
			c.sweep(q, resp)
		}
		req = c.next(room, q)
	}
}

func (c *connection) sweep(q *roomQueue, failed *chatimv1.ServerResponse) {
	c.mu.Lock()
	queued := len(q.waiting)
	c.mu.Unlock()
	for range queued {
		c.answerSwept(c.pop(q), failed)
	}
}

func (c *connection) answerSwept(req *chatimv1.ClientRequest, failed *chatimv1.ServerResponse) {
	defer c.release()
	c.respond(&chatimv1.ServerResponse{Id: req.GetId(), Code: failed.GetCode(), Message: failed.GetMessage()})
}

func (c *connection) pop(q *roomQueue) *chatimv1.ClientRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	req := q.waiting[0]
	q.waiting[0] = nil
	q.waiting = q.waiting[1:]
	return req
}

func (c *connection) next(room uint64, q *roomQueue) *chatimv1.ClientRequest {
	c.mu.Lock()
	empty := len(q.waiting) == 0
	if empty {
		delete(c.rooms, room)
	}
	c.mu.Unlock()
	if empty {
		return nil
	}
	return c.pop(q)
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
