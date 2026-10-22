package main

import (
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const roomsCID = "e2e-rooms-"

func (r *roomRun) sendAll(room, user string, from, to int) ([]e2e.Want, uint64, error) {
	var wants []e2e.Want
	var last uint64
	for i := from; i <= to; i++ {
		seq, err := r.send(room, user, roomsCID+"r1-"+strconv.Itoa(i), nil)
		if err != nil {
			return nil, 0, err
		}
		last = seq
		wants = append(wants, r.sent(room, seq, nil))
	}
	return wants, last, nil
}

func (r *roomRun) unreadFromPeer() error {
	wants, last, err := r.sendAll(r.list[0], r.peer, 1, 3)
	if err != nil {
		return err
	}
	if err := r.expect(wants...); err != nil {
		return err
	}
	return r.first("unread after three messages from "+r.peer, r.text(r.list[0], 3, last, r.peer))
}

func (r *roomRun) systemMessages() error {
	room := r.list[1]
	_, err := r.send(room, r.bot, roomsCID+"bot-text", nil)
	if err := refused("a text message from the non-member "+r.bot, codes.PermissionDenied, err); err != nil {
		return err
	}
	for i, skip := range []bool{false, true} {
		cid := "sys-" + roomsCID + strconv.Itoa(i+1)
		seq, err := r.send(room, r.bot, cid, func(req *chatimv1.SendMessageRequest) {
			req.Kind, req.SkipUnread, req.Meta = chatimv1.MessageKind_MESSAGE_KIND_SYSTEM, skip, []byte(sysMeta)
		})
		if err != nil {
			return err
		}
		r.sys = seq
		extras := &chatimv1.Message{Kind: chatimv1.MessageKind_MESSAGE_KIND_SYSTEM, SkipUnread: skip, Meta: []byte(sysMeta)}
		if err := r.expect(r.sent(room, seq, extras)); err != nil {
			return err
		}
		want := e2e.RoomLine{Room: room, Unread: 1, Preview: seq, Sender: r.bot, Kind: "system"}
		if err := r.first(fmt.Sprintf("room list after system message %d (skip_unread %v)", i+1, skip), want); err != nil {
			return err
		}
	}
	return nil
}

func (r *roomRun) readDeleteHide() error {
	room := r.list[0]
	read, _, err := r.cl.MarkRead(r.as(r.me), &chatimv1.MarkReadRequest{RoomId: room})
	if err == nil {
		err = e2e.CheckReadReply("mark read", 3, 2, read.GetReadSeq(), read.GetReadVer())
	}
	if err != nil {
		return fmt.Errorf("mark room %s read as %s: %w", room, r.me, err)
	}
	if err := r.expect(r.want(room, e2e.KindRead, e2e.ReadWantID(room, r.me, 2), e2e.ReadPayload(3, 2))); err != nil {
		return err
	}
	if err := r.line("unread after reading", r.text(room, 0, 3, r.peer)); err != nil {
		return err
	}
	wants, last, err := r.sendAll(room, r.peer, 4, 5)
	if err == nil {
		err = r.expect(wants...)
	}
	if err == nil {
		err = r.line("unread after two more messages", r.text(room, 2, last, r.peer))
	}
	if err != nil {
		return err
	}
	if _, _, err := r.cl.DeleteMessage(r.as(r.peer), &chatimv1.DeleteMessageRequest{RoomId: room, Seq: last - 1}); err != nil {
		return fmt.Errorf("delete seq %d: %w", last-1, err)
	}
	if err := r.expect(r.want(room, e2e.KindDeleted, e2e.ChangeEventID(room, last-1, 1), "")); err != nil {
		return err
	}
	if err := r.line("unread after a delete in the unread range", r.text(room, 1, last, r.peer)); err != nil {
		return err
	}
	if _, _, err := r.cl.HideMessage(r.as(r.me), &chatimv1.HideMessageRequest{RoomId: room, Seq: last}); err != nil {
		return fmt.Errorf("hide seq %d: %w", last, err)
	}
	if err := r.expect(r.want(room, e2e.KindHidden, e2e.HiddenEventID(room, r.me, 0, last), e2e.HiddenPayload(0, last))); err != nil {
		return err
	}
	return r.line("unread after a hide in the unread range", r.text(room, 0, last-1, r.peer))
}

func (r *roomRun) setPreferences(room string, req *chatimv1.SetRoomPreferencesRequest, payload string) error {
	req.RoomId = room
	for i, want := range []bool{true, false} {
		resp, _, err := r.cl.SetRoomPreferences(r.as(r.me), req)
		switch {
		case err != nil:
			return fmt.Errorf("set preferences of room %s (call %d): %w", room, i+1, err)
		case resp.GetChanged() != want || e2e.StatePayload(resp.GetState()) != payload:
			return fmt.Errorf("set preferences of room %s (call %d) = changed %v %s, want changed %v %s", room, i+1, resp.GetChanged(), e2e.StatePayload(resp.GetState()), want, payload)
		}
	}
	return r.expect(r.want(room, e2e.KindPreferences, e2e.PreferencesEventID(room, r.me, 1), payload))
}

func (r *roomRun) pinMovesFirst() error {
	pinned := true
	if err := r.setPreferences(r.list[2], &chatimv1.SetRoomPreferencesRequest{Pinned: &pinned}, e2e.PreferencesPayload(true, false, false, 1)); err != nil {
		return err
	}
	return r.first("room list after pinning", e2e.RoomLine{Room: r.list[2]})
}

func (r *roomRun) archiveMovesFolder() error {
	archived, room := true, r.list[1]
	if err := r.setPreferences(room, &chatimv1.SetRoomPreferencesRequest{Archived: &archived}, e2e.PreferencesPayload(false, false, true, 1)); err != nil {
		return err
	}
	main := []string{r.list[2], r.list[0], r.list[3]}
	if err := r.awaitList("main rooms after archiving", chatimv1.RoomFolder_ROOM_FOLDER_MAIN, func(items []*chatimv1.RoomItem) error {
		return e2e.CheckOrder("main folder", items, main)
	}); err != nil {
		return err
	}
	return r.awaitList("archived rooms", chatimv1.RoomFolder_ROOM_FOLDER_ARCHIVED, func(items []*chatimv1.RoomItem) error {
		if err := e2e.CheckOrder("archived folder", items, []string{room}); err != nil {
			return err
		}
		return e2e.CheckFirst(items, e2e.RoomLine{Room: room, Unread: 1, Preview: r.sys, Sender: r.bot, Kind: "system"})
	})
}

func (r *roomRun) secondPage() error {
	main := chatimv1.RoomFolder_ROOM_FOLDER_MAIN
	return r.settle("two pages of two", func() (error, error) {
		first, err := r.myRooms(main, "", 2)
		if err != nil {
			return nil, err
		}
		if first.GetNext() == "" {
			return fmt.Errorf("first page %v has no next cursor", e2e.RoomIDsOf(first.GetItems())), nil
		}
		second, err := r.myRooms(main, first.GetNext(), 2)
		if err != nil {
			return nil, err
		}
		if second.GetNext() != "" {
			return fmt.Errorf("second page %v has next %q, want the end", e2e.RoomIDsOf(second.GetItems()), second.GetNext()), nil
		}
		return e2e.CheckOrder("two pages of two", append(first.GetItems(), second.GetItems()...), []string{r.list[2], r.list[0], r.list[3]}), nil
	})
}
