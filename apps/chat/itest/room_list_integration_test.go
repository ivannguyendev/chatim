package itest

import (
	"context"
	"maps"
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itTailHitsSeries     = `chatim_chat_room_cache_hits_total{cache="tail"}`
	itRoomForgetsSeries  = "chatim_chat_room_watch_forgets_total"
	itRenamedRoomName    = "renamed on chat 2"
	itEditedPreviewText  = "second, edited on chat 2"
	itSecondPreviewText  = "second"
	itRoomListUnreadSent = 2
)

func myRoomItem(t *testing.T, client chatimv1.ChatServiceClient, user, roomID string) *chatimv1.RoomItem {
	t.Helper()
	resp, err := client.ListMyRooms(callerAs(t.Context(), user), &chatimv1.ListMyRoomsRequest{})
	if err != nil {
		t.Fatalf("ListMyRooms as %s: %v", user, err)
	}
	for _, it := range resp.GetItems() {
		if it.GetRoom().GetId() == roomID {
			return it
		}
	}
	t.Fatalf("ListMyRooms as %s = %v, want room %s", user, resp.GetItems(), roomID)
	return nil
}

func assertPreview(t *testing.T, item *chatimv1.RoomItem, seq uint64, text string, edited bool, unread uint32) {
	t.Helper()
	p := item.GetPreview()
	if p.GetSeq() != seq || p.GetText() != text || p.GetEdited() != edited || item.GetUnread().GetCount() != unread {
		t.Fatalf("room item = preview %v unread %v, want seq %d %q edited %v with %d unread", p, item.GetUnread(), seq, text, edited, unread)
	}
}

func TestRealInfraRoomListCachesOnOneChatNodeFollowWritesMadeOnTheOther(t *testing.T) {
	it := realInfra(t)
	chat1 := startChatNode(t, it, itFastSweep)
	env := maps.Clone(itFastSweep)
	env["CHAT_NODE_ID"] = "it-chat-2-" + it.suffix
	chat2 := startChatNode(t, it, env)
	awaitSlotsSplit(t, it, chat1.cfg.NodeID, chat2.cfg.NodeID)
	c1, c2 := dialChat(t, chat1.cfg), dialChat(t, chat2.cfg)
	st := itStore(it, chat1)
	roomID := createRoomWith(t, c1, []string{itUser, "bob"})
	room := parseRoom(t, roomID)

	first := sendAs(t, c2, "bob", roomID, "list-1", "first")
	awaitActivity(t, st, room, first)
	assertPreview(t, myRoomItem(t, c1, itUser, roomID), first, "first", false, 1)
	hits := itMetric(t, chat1.cfg, itTailHitsSeries)
	assertPreview(t, myRoomItem(t, c1, itUser, roomID), first, "first", false, 1)
	if got := itMetric(t, chat1.cfg, itTailHitsSeries); got <= hits {
		t.Fatalf("tail cache hits on chat 1 = %v after a repeat read, want more than %v", got, hits)
	}

	second := sendAs(t, c2, "bob", roomID, "list-2", itSecondPreviewText)
	awaitActivity(t, st, room, second)
	assertPreview(t, myRoomItem(t, c1, itUser, roomID), second, itSecondPreviewText, false, itRoomListUnreadSent)

	_, err := retryingUnavailable(callerAs(t.Context(), "bob"), func(ctx context.Context) (*chatimv1.EditMessageResponse, error) {
		return c2.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: roomID, Seq: second, Text: itEditedPreviewText})
	})
	if err != nil {
		t.Fatalf("EditMessage(%d) on chat 2: %v", second, err)
	}
	awaitStored(t, "last edit of the room", func() (domain.Room, error) { return st.Get(t.Context(), room) }, func(r domain.Room) bool { return !r.LastEditAt.IsZero() })
	assertPreview(t, myRoomItem(t, c1, itUser, roomID), second, itEditedPreviewText, true, itRoomListUnreadSent)

	forgets := itMetric(t, chat1.cfg, itRoomForgetsSeries)
	name := itRenamedRoomName
	resp, err := retryingUnavailable(caller(t.Context()), func(ctx context.Context) (*chatimv1.UpdateRoomResponse, error) {
		return c2.UpdateRoom(ctx, &chatimv1.UpdateRoomRequest{RoomId: roomID, Name: &name})
	})
	if err != nil || !resp.GetChanged() || resp.GetRoom().GetInfoVer() != 2 {
		t.Fatalf("UpdateRoom on chat 2 = %v, %v; want a change to info_ver 2", resp, err)
	}
	awaitStored(t, "room name on chat 1", func() (*chatimv1.Room, error) { return myRoomItem(t, c1, itUser, roomID).GetRoom(), nil }, func(r *chatimv1.Room) bool {
		return r.GetName() == name && r.GetInfoVer() == 2
	})
	if got := itMetric(t, chat1.cfg, itRoomForgetsSeries); got <= forgets {
		t.Fatalf("room watch forgets on chat 1 = %v, want more than %v after room_updated", got, forgets)
	}
}
