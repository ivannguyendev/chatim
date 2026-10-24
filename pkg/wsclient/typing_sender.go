package wsclient

import (
	"context"
	"sync"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const TypingInterval = 3 * time.Second

type typingSender struct {
	mu   sync.Mutex
	sent map[string]time.Time
}

func newTypingSender() typingSender {
	return typingSender{sent: map[string]time.Time{}}
}

func (c *Client) Typing(roomID string) error {
	c.typing.mu.Lock()
	defer c.typing.mu.Unlock()
	if sentAt, ok := c.typing.sent[roomID]; ok && time.Since(sentAt) < TypingInterval {
		return nil
	}
	if err := c.sendTyping(roomID, false); err != nil {
		return err
	}
	c.typing.sent[roomID] = time.Now()
	return nil
}

func (c *Client) StopTyping(roomID string) error {
	c.typing.mu.Lock()
	defer c.typing.mu.Unlock()
	if _, ok := c.typing.sent[roomID]; !ok {
		return nil
	}
	delete(c.typing.sent, roomID)
	return c.sendTyping(roomID, true)
}

func (c *Client) WatchPresence(userIDs []string) error {
	return c.sendFrame(context.Background(), &chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_WatchPresence{
		WatchPresence: &chatimv1.WatchPresenceFrame{UserIds: userIDs},
	}})
}

func (c *Client) sendTyping(roomID string, stopped bool) error {
	return c.sendFrame(context.Background(), &chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Typing{
		Typing: &chatimv1.TypingFrame{RoomId: roomID, Stopped: stopped},
	}})
}

func (c *Client) typingEndedBySend(req *chatimv1.ClientRequest) {
	send := req.GetSendMessage()
	if send == nil {
		return
	}
	c.typing.mu.Lock()
	delete(c.typing.sent, send.GetRoomId())
	c.typing.mu.Unlock()
}
