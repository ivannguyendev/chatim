package main

import (
	"errors"
	"fmt"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func (r *wsEventsRun) createGroup() error {
	resp, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_CreateRoom{CreateRoom: &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e websocket events", Members: []string{r.a.user}, RequestId: randomCID(),
	}}})
	if err != nil {
		return fmt.Errorf("create room: %w", err)
	}
	if r.room = resp.GetCreateRoom().GetRoom().GetId(); r.room == "" {
		return errors.New("create room returned no room id")
	}
	added := r.inRoom("member_added")
	added.User = r.a.user
	if _, err := r.a.await(0, added); err != nil {
		return err
	}
	return r.a.ws.OpenRoom(r.base, r.room)
}

func (r *wsEventsRun) addB() error {
	_, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_AddMembers{AddMembers: &chatimv1.AddMembersRequest{
		RoomId: r.room, Users: []string{r.b.user}, RequestId: randomCID(),
	}}})
	if err != nil {
		return fmt.Errorf("add %s: %w", r.b.user, err)
	}
	added := r.inRoom("member_added")
	added.User = r.b.user
	i, err := r.b.await(0, added)
	if err != nil {
		return err
	}
	if first, e, _ := r.b.feed.Find(0, e2e.WSMatch{Room: r.room}); first != i {
		return fmt.Errorf("peer B received %+v of group %s before its member_added", e, r.room)
	}
	return nil
}

func (r *wsEventsRun) sendSeen() error {
	seq, err := r.send("e2e websocket events first", nil)
	if err != nil {
		return err
	}
	r.seq = seq
	for _, p := range []*wsPeer{r.a, r.b} {
		if _, err := p.await(0, r.created(seq)); err != nil {
			return err
		}
	}
	return nil
}

func (r *wsEventsRun) reactions() error {
	settings, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_GetReactionSettings{GetReactionSettings: &chatimv1.GetReactionSettingsRequest{}}})
	if err != nil {
		return fmt.Errorf("reaction settings: %w", err)
	}
	emojis := settings.GetGetReactionSettings().GetEmojis()
	if len(emojis) < 2 {
		return errWSTwoEmojis
	}
	markB := r.b.feed.Len()
	if err := r.reactAndAwait(emojis[0]); err != nil {
		return err
	}
	if err := r.settle(); err != nil {
		return err
	}
	if err := r.b.absent(markB, r.inRoom("reaction_changed")); err != nil {
		return err
	}
	if err := r.b.ws.OpenRoom(r.base, r.room); err != nil {
		return fmt.Errorf("peer B opens %s: %w", r.room, err)
	}
	if _, err := r.b.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_GetReactionSettings{GetReactionSettings: &chatimv1.GetReactionSettingsRequest{}}}); err != nil {
		return fmt.Errorf("peer B barrier after open_room: %w", err)
	}
	markB = r.b.feed.Len()
	if err := r.reactAndAwait(emojis[1]); err != nil {
		return err
	}
	_, err = r.b.await(markB, r.inRoom("reaction_changed"))
	return err
}

func (r *wsEventsRun) reactAndAwait(emoji string) error {
	markA := r.a.feed.Len()
	if _, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_ReactMessage{ReactMessage: &chatimv1.ReactMessageRequest{
		RoomId: r.room, Seq: r.seq, Emoji: emoji,
	}}}); err != nil {
		return fmt.Errorf("react %s: %w", emoji, err)
	}
	_, err := r.a.await(markA, r.inRoom("reaction_changed"))
	return err
}

func (r *wsEventsRun) ownerOnly() error {
	markA := r.a.feed.Len()
	if _, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_SetBookmark{SetBookmark: &chatimv1.SetBookmarkRequest{
		RoomId: r.room, Seq: r.seq, On: true,
	}}}); err != nil {
		return fmt.Errorf("bookmark: %w", err)
	}
	if _, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_HideMessage{HideMessage: &chatimv1.HideMessageRequest{
		RoomId: r.room, Seq: r.seq,
	}}}); err != nil {
		return fmt.Errorf("hide: %w", err)
	}
	private := []string{"bookmark_changed", "message_hidden"}
	for _, kind := range private {
		if _, err := r.a.await(markA, r.inRoom(kind)); err != nil {
			return err
		}
	}
	if err := r.settle(); err != nil {
		return err
	}
	for _, kind := range private {
		if err := r.b.absent(0, r.inRoom(kind)); err != nil {
			return err
		}
	}
	return nil
}

func (r *wsEventsRun) mentions() error {
	userSeq, err := r.send("e2e websocket events @B", targets(userKind, r.b.user))
	if err != nil {
		return err
	}
	allSeq, err := r.send("e2e websocket events @all", &chatimv1.MentionSet{All: true})
	if err != nil {
		return err
	}
	want := []e2e.WSMatch{
		r.created(userSeq), r.created(allSeq),
		{Frame: e2e.FrameNotification, ID: e2e.NotifiedID(r.room, userSeq, 0, e2e.UserTarget(r.b.user))},
		{Frame: e2e.FrameNotification, ID: e2e.NotifiedID(r.room, allSeq, 0, e2e.AllTarget)},
	}
	for _, m := range want {
		if _, err := r.b.await(0, m); err != nil {
			return err
		}
	}
	if err := r.settle(); err != nil {
		return err
	}
	for _, m := range want {
		if err := r.once(r.b, m); err != nil {
			return err
		}
	}
	r.notes = append(r.notes, "notifications "+want[2].ID+" and "+want[3].ID+" once each")
	return nil
}

func (r *wsEventsRun) removeB() error {
	markB := r.b.feed.Len()
	if _, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_RemoveMember{RemoveMember: &chatimv1.RemoveMemberRequest{
		RoomId: r.room, User: r.b.user,
	}}}); err != nil {
		return fmt.Errorf("remove %s: %w", r.b.user, err)
	}
	removed := r.inRoom("member_removed")
	removed.User = r.b.user
	i, err := r.b.await(markB, removed)
	if err != nil {
		return err
	}
	closed := e2e.WSMatch{Frame: e2e.FrameRoomClosed, Kind: chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED.String(), Room: r.room}
	if _, err := r.b.await(i+1, closed); err != nil {
		return err
	}
	markB = r.b.feed.Len()
	seq, err := r.send("e2e websocket events after B left", nil)
	if err != nil {
		return err
	}
	if _, err := r.a.await(0, r.created(seq)); err != nil {
		return err
	}
	if err := r.settle(); err != nil {
		return err
	}
	return r.b.absent(markB, e2e.WSMatch{Room: r.room})
}
