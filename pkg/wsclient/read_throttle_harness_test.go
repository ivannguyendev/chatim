package wsclient

import (
	"strconv"
	"sync"
	"testing"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type readServer struct {
	*fakeServer
	t      *testing.T
	mu     sync.Mutex
	conn   *gws.Conn
	hold   bool
	held   []*chatimv1.ServerResponse
	failOn map[uint64]int32
	log    []string
}

func newReadServer(t *testing.T) *readServer {
	s := &readServer{t: t, failOn: map[uint64]int32{}}
	s.fakeServer = newFakeServer(s.onFrame)
	return s
}

func (s *readServer) onFrame(conn *gws.Conn, frame *chatimv1.ClientFrame) {
	s.mu.Lock()
	s.conn = conn
	line := frameLine(frame)
	if line != "" {
		s.log = append(s.log, line)
	}
	req := frame.GetRequest()
	if req == nil {
		s.mu.Unlock()
		return
	}
	resp := &chatimv1.ServerResponse{Id: req.GetId(), Code: s.failOn[req.GetMarkRead().GetSeq()]}
	if s.hold {
		s.held = append(s.held, resp)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	respond(s.t, conn, resp)
}

func (s *readServer) holding(on bool) {
	s.mu.Lock()
	s.hold = on
	s.mu.Unlock()
}

func (s *readServer) release() {
	s.mu.Lock()
	held, conn := s.held, s.conn
	s.held, s.hold = nil, false
	s.mu.Unlock()
	for _, resp := range held {
		respond(s.t, conn, resp)
	}
}

func (s *readServer) fail(seq uint64, code int32) {
	s.mu.Lock()
	s.failOn[seq] = code
	s.mu.Unlock()
}

func (s *readServer) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.log
	s.log = nil
	return out
}

func frameLine(frame *chatimv1.ClientFrame) string {
	if room := frame.GetCloseRoom(); room != nil {
		return "close " + room.GetRoomId()
	}
	req := frame.GetRequest()
	switch {
	case req.GetMarkRead() != nil:
		return "read " + req.GetMarkRead().GetRoomId() + " " + strconv.FormatUint(req.GetMarkRead().GetSeq(), 10)
	case req.GetMarkUnread() != nil:
		return "unread " + req.GetMarkUnread().GetRoomId() + " " + strconv.FormatUint(req.GetMarkUnread().GetSeq(), 10)
	case req.GetListMyRooms() != nil:
		return "list"
	case req.GetGetRooms() != nil:
		return "get-rooms"
	default:
		return ""
	}
}

func listRooms() *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_ListMyRooms{ListMyRooms: &chatimv1.ListMyRoomsRequest{}}}
}

func readsOf(room string, users ...string) *chatimv1.Event {
	ev := &chatimv1.Event{RoomId: room, Payload: &chatimv1.Event_ReadPositions{ReadPositions: &chatimv1.ReadPositions{}}}
	for i := 0; i+1 < len(users); i += 2 {
		ver, _ := strconv.ParseUint(users[i+1], 10, 64)
		ev.GetReadPositions().Entries = append(ev.GetReadPositions().Entries, &chatimv1.ReadEntry{User: users[i], ReadSeq: ver * 10, ReadVer: ver})
		ev.Id += users[i] + ":" + users[i+1] + ";"
	}
	ev.Id = room + "-rp-" + ev.Id
	return ev
}
