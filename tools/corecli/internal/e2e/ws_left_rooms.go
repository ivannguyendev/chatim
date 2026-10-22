package e2e

import (
	"fmt"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func CheckLeftRoom(resp *chatimv1.ListMyRoomIDsResponse, room string, ver uint32) error {
	for _, r := range resp.GetRooms() {
		if r.GetRoomId() == room {
			return fmt.Errorf("room %s still listed as joined at ver %d", room, r.GetVer())
		}
	}
	for _, r := range resp.GetLeft() {
		if r.GetRoomId() != room {
			continue
		}
		if r.GetVer() != ver {
			return fmt.Errorf("room %s left at ver %d, want %d", room, r.GetVer(), ver)
		}
		return nil
	}
	return fmt.Errorf("room %s missing from %d left rooms", room, len(resp.GetLeft()))
}
