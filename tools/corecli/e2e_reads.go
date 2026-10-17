package main

import (
	"fmt"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func (r *roomRun) getRoomsRefreshes() error {
	asked := []string{r.list[0], r.list[1], r.st.Room}
	var settled *chatimv1.RoomItem
	err := r.awaitRooms("get rooms "+fmt.Sprint(asked), asked, func(resp *chatimv1.GetRoomsResponse) error {
		if !slices.Equal(resp.GetMissing(), []string{r.st.Room}) {
			return fmt.Errorf("get rooms reports missing %v, want only %s", resp.GetMissing(), r.st.Room)
		}
		if err := e2e.CheckOrder("get rooms", resp.GetItems(), asked[:2]); err != nil {
			return err
		}
		items := resp.GetItems()
		if err := e2e.CheckLine(items, r.text(r.list[0], 0, 4, r.peer)); err != nil {
			return err
		}
		if err := r.checkRenewed(items[0]); err != nil {
			return err
		}
		settled = items[0]
		return e2e.CheckLine(items, e2e.RoomLine{Room: r.list[1], Unread: 1, Preview: r.sys, Sender: r.bot, Kind: "system"})
	})
	if err != nil {
		return err
	}
	return r.awaitList("the listed item of room "+r.list[0], chatimv1.RoomFolder_ROOM_FOLDER_MAIN, func(items []*chatimv1.RoomItem) error {
		i := slices.IndexFunc(items, func(it *chatimv1.RoomItem) bool { return it.GetRoom().GetId() == r.list[0] })
		if i < 0 {
			return fmt.Errorf("room list %v misses room %s", e2e.RoomIDsOf(items), r.list[0])
		}
		if !proto.Equal(items[i], settled) {
			return fmt.Errorf("room list item %v, want the GetRooms item %v", items[i], settled)
		}
		return nil
	})
}

func (r *roomRun) checkRenewed(it *chatimv1.RoomItem) error {
	me, p := it.GetMe(), it.GetPreview()
	if it.GetRoom().GetName() != "e2e rooms 1" || me.GetRole() != chatimv1.MemberRole_MEMBER_ROLE_OWNER || me.GetReadSeq() != 3 || !p.GetDeleted() || p.GetText() != "" {
		return fmt.Errorf("room %s = %v, want the owner at read_seq 3 with deleted seq 4 as preview", r.list[0], it)
	}
	return nil
}

func (r *roomRun) updateRoom() error {
	room, name := r.list[0], "e2e renamed "+r.list[0]
	req := &chatimv1.UpdateRoomRequest{RoomId: room, Name: &name}
	_, _, err := r.cl.UpdateRoom(r.as(r.peer), req)
	if err := refused("rename by the member "+r.peer, codes.PermissionDenied, err); err != nil {
		return err
	}
	for i, want := range []bool{true, false} {
		resp, _, err := r.cl.UpdateRoom(r.as(r.me), req)
		switch {
		case err != nil:
			return fmt.Errorf("rename room %s (call %d): %w", room, i+1, err)
		case resp.GetChanged() != want || resp.GetRoom().GetName() != name || resp.GetRoom().GetInfoVer() != 2:
			return fmt.Errorf("rename room %s (call %d) = %v changed %v, want %q at info_ver 2 changed %v", room, i+1, resp.GetRoom(), resp.GetChanged(), name, want)
		}
	}
	if err := r.expect(r.want(room, e2e.KindRoomUpdated, e2e.RoomInfoEventID(room, 2), e2e.RoomInfoPayload(name, "", 2))); err != nil {
		return err
	}
	return r.awaitRooms("the new name of room "+room, []string{room}, func(resp *chatimv1.GetRoomsResponse) error {
		if got := resp.GetItems(); len(got) != 1 || got[0].GetRoom().GetName() != name || got[0].GetRoom().GetInfoVer() != 2 {
			return fmt.Errorf("get rooms gave %v, want name %q at info_ver 2", got, name)
		}
		return nil
	})
}

func (r *roomRun) aroundAndMessages() error {
	room := r.list[0]
	around, _, err := r.cl.GetHistory(r.as(r.me), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND, Seq: 3, Limit: 4})
	if err != nil {
		return fmt.Errorf("history around seq 3: %w", err)
	}
	msgs := around.GetMessages()
	if err := e2e.CheckSeqs("history around seq 3", room, msgs, []uint64{2, 3, 4, 5}); err != nil {
		return err
	}
	if !msgs[2].GetDeleted() || !msgs[3].GetHidden() || msgs[1].GetText() != e2e.TextFor(roomsCID+"r1-3") {
		return fmt.Errorf("history around seq 3 = %v, want seq 3 readable, seq 4 deleted and seq 5 hidden", msgs)
	}
	asked, _, err := r.cl.GetMessages(r.as(r.me), &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{3, 1, 99}})
	if err != nil {
		return fmt.Errorf("messages 3, 1 and 99: %w", err)
	}
	return e2e.CheckSeqs("messages 3, 1 and 99", room, asked.GetMessages(), []uint64{1, 3})
}

func (r *roomRun) threadAfterTwoReplies() error {
	room := r.list[3]
	parent, err := r.send(room, r.me, roomsCID+"parent", nil)
	if err != nil {
		return err
	}
	r.parent = parent
	links := &chatimv1.Message{ReplyTo: &chatimv1.ReplyRef{Seq: parent}}
	wants := []e2e.Want{r.sent(room, parent, nil)}
	for _, n := range []string{"1", "2"} {
		seq, err := r.send(room, r.peer, roomsCID+"reply-"+n, func(req *chatimv1.SendMessageRequest) { req.ReplyTo = links.GetReplyTo() })
		if err != nil {
			return err
		}
		r.replies = append(r.replies, seq)
		wants = append(wants, r.sent(room, seq, links))
	}
	wants = append(wants, r.want(room, e2e.KindCounts, e2e.RepliesEventID(room, parent, 2), e2e.RepliesPayload(2)))
	if err := r.expect(wants...); err != nil {
		return err
	}
	return r.settle("threads of room "+room, func() (error, error) {
		resp, _, err := r.cl.ListThreads(r.as(r.me), &chatimv1.ListThreadsRequest{RoomId: room})
		if err != nil {
			return nil, fmt.Errorf("threads of room %s: %w", room, err)
		}
		last := r.replies[len(r.replies)-1]
		if got := resp.GetItems(); len(got) != 1 || got[0].GetMessage().GetSeq() != parent || got[0].GetLastReplyPreview().GetSeq() != last {
			return fmt.Errorf("threads = %v, want parent seq %d with last reply seq %d", got, parent, last), nil
		}
		return nil, nil
	})
}
