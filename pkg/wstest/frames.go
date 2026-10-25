package wstest

import (
	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Conn) OpenRoom(roomID string) error {
	return c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_OpenRoom{OpenRoom: &chatimv1.OpenRoomFrame{RoomId: roomID}}})
}

func (c *Conn) LoadRooms() error {
	return c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_LoadRooms{LoadRooms: &chatimv1.LoadRoomsFrame{}}})
}

func (c *Conn) Typing(roomID string, stopped bool) error {
	return c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Typing{Typing: &chatimv1.TypingFrame{RoomId: roomID, Stopped: stopped}}})
}

func (c *Conn) WatchPresence(userIDs []string) error {
	return c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_WatchPresence{WatchPresence: &chatimv1.WatchPresenceFrame{UserIds: userIDs}}})
}

type events struct {
	gws.BuiltinEventHandler
	c *Conn
}

func (e events) OnMessage(_ *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	if message.Opcode != gws.OpcodeBinary {
		return
	}
	frame := &chatimv1.ServerFrame{}
	if err := proto.Unmarshal(message.Bytes(), frame); err != nil {
		return
	}
	if resp := frame.GetResponse(); resp != nil {
		e.c.deliver(resp)
		return
	}
	e.c.queue.push(frame)
}

func (e events) OnClose(_ *gws.Conn, err error) {
	e.c.finish(closeErrorOf(err))
}
