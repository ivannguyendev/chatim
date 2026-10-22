package wsclient

import (
	"context"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) OpenRoom(ctx context.Context, roomID string) error {
	return c.sendFrame(ctx, &chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_OpenRoom{
		OpenRoom: &chatimv1.OpenRoomFrame{RoomId: roomID},
	}})
}

func (c *Client) CloseRoom(ctx context.Context, roomID string) error {
	return c.sendFrame(ctx, &chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_CloseRoom{
		CloseRoom: &chatimv1.CloseRoomFrame{RoomId: roomID},
	}})
}

func (c *Client) Applier() *Applier { return c.opts.applier }

func (c *Client) sendFrame(ctx context.Context, frame *chatimv1.ClientFrame) error {
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return c.send(ctx, payload)
}

func (c *Client) enqueue(frame *chatimv1.ServerFrame) {
	select {
	case c.inbox <- frame:
	default:
		select {
		case c.backlog <- struct{}{}:
		default:
		}
	}
}

func (c *Client) dispatchLoop() {
	defer close(c.dispatched)
	for {
		select {
		case frame := <-c.inbox:
			c.dispatch(frame)
		case <-c.backlog:
			c.resync(chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG)
		case <-c.done:
			c.drainInbox()
			return
		}
	}
}

func (c *Client) drainInbox() {
	for {
		select {
		case frame := <-c.inbox:
			c.dispatch(frame)
		default:
			return
		}
	}
}

func (c *Client) dispatch(frame *chatimv1.ServerFrame) {
	switch f := frame.GetFrame().(type) {
	case *chatimv1.ServerFrame_Event:
		c.applyEvent(f.Event)
	case *chatimv1.ServerFrame_Notification:
		if c.opts.onNotify != nil {
			c.opts.onNotify(f.Notification)
		}
	case *chatimv1.ServerFrame_Ready:
		if c.opts.onReady != nil {
			c.opts.onReady()
		}
	case *chatimv1.ServerFrame_Resync:
		c.resync(f.Resync.GetReason())
	case *chatimv1.ServerFrame_RoomClosed:
		if c.opts.onRoomClosed != nil {
			c.opts.onRoomClosed(f.RoomClosed.GetRoomId(), f.RoomClosed.GetReason())
		}
	}
}

func (c *Client) applyEvent(ev *chatimv1.Event) {
	outcome := c.opts.applier.Apply(ev)
	if outcome.Verdict != VerdictDeliver {
		return
	}
	if !outcome.Gap.Empty() && c.opts.onGap != nil {
		c.opts.onGap(c, outcome.Gap)
	}
	if c.opts.onEvent != nil {
		c.opts.onEvent(ev)
	}
}

func (c *Client) resync(reason chatimv1.ResyncReason) {
	if c.opts.onResync != nil {
		c.opts.onResync(reason)
	}
}

func dispatched(frame *chatimv1.ServerFrame) bool {
	switch frame.GetFrame().(type) {
	case *chatimv1.ServerFrame_Event, *chatimv1.ServerFrame_Notification, *chatimv1.ServerFrame_Ready,
		*chatimv1.ServerFrame_Resync, *chatimv1.ServerFrame_RoomClosed:
		return true
	default:
		return false
	}
}
