package wsclient

import (
	"context"
	"sync"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReadInterval = time.Second

type readRoom struct {
	pending uint64
	waiting bool
	timer   *time.Timer
}

func (r *readRoom) hold(seq uint64) {
	switch {
	case !r.waiting:
		r.pending = seq
	case r.pending == 0 || seq == 0:
		r.pending = 0
	default:
		r.pending = max(r.pending, seq)
	}
	r.waiting = true
}

func (r *readRoom) take() (uint64, bool) {
	seq, waiting := r.pending, r.waiting
	r.pending, r.waiting = 0, false
	return seq, waiting
}

type readThrottle struct {
	mu       sync.Mutex
	rooms    map[string]*readRoom
	inflight int
	idle     chan struct{}
	stopped  bool
}

func newReadThrottle() readThrottle {
	idle := make(chan struct{})
	close(idle)
	return readThrottle{rooms: map[string]*readRoom{}, idle: idle}
}

func (c *Client) MarkRead(ctx context.Context, roomID string, seq uint64) error {
	c.reads.mu.Lock()
	defer c.reads.mu.Unlock()
	if c.reads.stopped {
		return c.closedErr()
	}
	if r, ok := c.reads.rooms[roomID]; ok {
		r.hold(seq)
		return nil
	}
	if err := c.sendReadLocked(ctx, roomID, seq); err != nil {
		return err
	}
	c.reads.rooms[roomID] = &readRoom{timer: time.AfterFunc(ReadInterval, func() { c.readTick(roomID) })}
	return nil
}

func (c *Client) MarkUnread(ctx context.Context, roomID string, seq uint64) (*chatimv1.MarkUnreadResponse, error) {
	req := &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_MarkUnread{MarkUnread: &chatimv1.MarkUnreadRequest{RoomId: roomID, Seq: seq}}}
	c.reads.mu.Lock()
	if r, ok := c.reads.rooms[roomID]; ok {
		r.take()
	}
	id, reply, err := c.start(ctx, req)
	c.reads.mu.Unlock()
	if err != nil {
		return nil, err
	}
	resp, err := c.wait(ctx, id, reply)
	return resp.GetMarkUnread(), err
}

func (c *Client) CloseRoom(ctx context.Context, roomID string) error {
	c.reads.mu.Lock()
	defer c.reads.mu.Unlock()
	if r, ok := c.reads.rooms[roomID]; ok {
		if seq, waiting := r.take(); waiting {
			if err := c.sendReadLocked(ctx, roomID, seq); err != nil {
				return err
			}
		}
	}
	return c.sendFrame(ctx, &chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_CloseRoom{
		CloseRoom: &chatimv1.CloseRoomFrame{RoomId: roomID},
	}})
}

func (c *Client) readTick(roomID string) {
	c.reads.mu.Lock()
	r, ok := c.reads.rooms[roomID]
	if c.reads.stopped || !ok {
		c.reads.mu.Unlock()
		return
	}
	seq, waiting := r.take()
	if !waiting {
		delete(c.reads.rooms, roomID)
		c.reads.mu.Unlock()
		return
	}
	err := c.sendReadLocked(context.Background(), roomID, seq)
	if err == nil {
		r.timer = time.AfterFunc(ReadInterval, func() { c.readTick(roomID) })
	} else {
		delete(c.reads.rooms, roomID)
	}
	c.reads.mu.Unlock()
	if err != nil {
		c.readFailed(roomID, err)
	}
}

func (c *Client) flushReads(ctx context.Context) error {
	c.reads.mu.Lock()
	for roomID, r := range c.reads.rooms {
		if seq, waiting := r.take(); waiting {
			if err := c.sendReadLocked(ctx, roomID, seq); err != nil {
				c.reads.mu.Unlock()
				return err
			}
		}
	}
	idle := c.reads.idle
	c.reads.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return nil
	}
}

func (c *Client) sendReadLocked(ctx context.Context, roomID string, seq uint64) error {
	req := &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_MarkRead{MarkRead: &chatimv1.MarkReadRequest{RoomId: roomID, Seq: seq}}}
	_, reply, err := c.start(ctx, req)
	if err != nil {
		return err
	}
	if c.reads.inflight == 0 {
		c.reads.idle = make(chan struct{})
	}
	c.reads.inflight++
	c.readers.Go(func() { c.awaitRead(roomID, reply) })
	return nil
}

func (c *Client) awaitRead(roomID string, reply chan *chatimv1.ServerResponse) {
	var err error
	select {
	case resp := <-reply:
		err = responseError(resp)
	case <-c.done:
		select {
		case resp := <-reply:
			err = responseError(resp)
		default:
		}
	}
	c.reads.mu.Lock()
	c.reads.inflight--
	if c.reads.inflight == 0 {
		close(c.reads.idle)
	}
	c.reads.mu.Unlock()
	if err != nil {
		c.readFailed(roomID, err)
	}
}

func (c *Client) readFailed(roomID string, err error) {
	select {
	case <-c.done:
		return
	default:
	}
	if c.opts.onReadError != nil {
		c.opts.onReadError(roomID, err)
	}
}

func (c *Client) stopReads() {
	c.reads.mu.Lock()
	c.reads.stopped = true
	for _, r := range c.reads.rooms {
		r.timer.Stop()
	}
	clear(c.reads.rooms)
	c.reads.mu.Unlock()
	c.readers.Wait()
}

func (c *Client) closedErr() error {
	if err := c.Err(); err != nil {
		return err
	}
	return ErrClosed
}

func listsRooms(req *chatimv1.ClientRequest) bool {
	switch req.GetRequest().(type) {
	case *chatimv1.ClientRequest_ListMyRooms, *chatimv1.ClientRequest_GetRooms:
		return true
	default:
		return false
	}
}
