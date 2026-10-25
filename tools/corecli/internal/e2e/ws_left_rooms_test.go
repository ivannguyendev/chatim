package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestCheckLeftRoomAcceptsTheRoomOnlyInLeftWithItsVersion(t *testing.T) {
	resp := &chatimv1.ListMyRoomIDsResponse{
		Rooms: []*chatimv1.MyRoomID{{RoomId: "8", Ver: 1}},
		Left:  []*chatimv1.MyRoomID{{RoomId: "6", Ver: 4}, {RoomId: "7", Ver: 2}},
	}
	if err := e2e.CheckLeftRoom(resp, "7", 2); err != nil {
		t.Fatalf("CheckLeftRoom: %v", err)
	}
}

func TestCheckLeftRoomRefusesAWrongVersionAMissingRoomOrAJoinedOne(t *testing.T) {
	cases := map[string]*chatimv1.ListMyRoomIDsResponse{
		"wrong version": {Left: []*chatimv1.MyRoomID{{RoomId: "7", Ver: 1}}},
		"missing":       {Left: []*chatimv1.MyRoomID{{RoomId: "6", Ver: 2}}},
		"still joined":  {Rooms: []*chatimv1.MyRoomID{{RoomId: "7", Ver: 3}}, Left: []*chatimv1.MyRoomID{{RoomId: "7", Ver: 2}}},
	}
	for name, resp := range cases {
		if err := e2e.CheckLeftRoom(resp, "7", 2); err == nil {
			t.Errorf("%s: CheckLeftRoom accepted %v", name, resp)
		}
	}
}
