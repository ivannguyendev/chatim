package session

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type roomLoader struct {
	mu    sync.Mutex
	rooms map[string][]interest.Room
	err   error
	calls atomic.Int64
}

func (l *roomLoader) Load(_ context.Context, tenant, user string) (interest.Membership, error) {
	l.calls.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	return interest.Membership{Joined: l.rooms[tenant+"/"+user]}, l.err
}

func (l *roomLoader) set(tenant, user string, rooms ...interest.Room) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rooms[tenant+"/"+user] = rooms
}

func (l *roomLoader) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

func newTestInterest(t *testing.T) *interest.Table {
	return newInterestWith(t, &roomLoader{rooms: map[string][]interest.Room{}})
}

func newInterestWith(t *testing.T, loader interest.Loader) *interest.Table {
	t.Helper()
	rooms, err := interest.New(interest.Config{Inflight: 4, OpenPending: 10 * time.Second, ReloadRate: 1000, UserConnectionsMax: 10}, loader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("interest.New: %v", err)
	}
	t.Cleanup(rooms.Close)
	return rooms
}

func (h *harness) token(user string, ttl time.Duration) string {
	return h.verifier.issue("token-"+user+"-"+time.Now().Add(ttl).String(), "t1", user, time.Now().Add(ttl))
}

func roomEvent(kind, data string, room uint64, id string) feed.Envelope {
	roomID := strconv.FormatUint(room, 10)
	raw, err := proto.Marshal(&chatimv1.Event{Id: id, Tenant: "t1", RoomId: roomID})
	if err != nil {
		panic(err)
	}
	return feed.Envelope{Tenant: "t1", DataKind: data, Room: roomID, Kind: kind, ID: id, Data: raw}
}

func (tc *testClient) openRoom(room string) {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_OpenRoom{OpenRoom: &chatimv1.OpenRoomFrame{RoomId: room}}})
}

func (tc *testClient) loadRooms() {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_LoadRooms{LoadRooms: &chatimv1.LoadRoomsFrame{}}})
}

func (tc *testClient) closeRoom(room string) {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_CloseRoom{CloseRoom: &chatimv1.CloseRoomFrame{RoomId: room}}})
}

func (tc *testClient) nextFrame() *chatimv1.ServerFrame {
	tc.t.Helper()
	select {
	case f := <-tc.frames:
		return f
	case <-tc.done:
		tc.t.Fatalf("connection closed with %d while waiting for a frame", tc.closeCode)
		return nil
	}
}

func (tc *testClient) noFrame() {
	tc.t.Helper()
	select {
	case f := <-tc.frames:
		tc.t.Fatalf("unexpected frame %v", f)
	default:
	}
}

func (h *harness) interestUsers() float64 {
	for _, s := range h.rooms.Sources() {
		if s.Name == "interest_users" {
			return s.Read()
		}
	}
	h.t.Fatal("no interest_users metric")
	return 0
}
