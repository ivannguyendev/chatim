package main

import (
	"errors"
	"fmt"
	"slices"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/chatcli/internal/e2e"
)

func (r *mediaRun) connect() error {
	for _, p := range []*wsPeer{r.a, r.b} {
		token, err := devSign(r.key, r.tenant, p.user, time.Now(), devTokenTTL)
		if err != nil {
			return err
		}
		r.tokens = append(r.tokens, token)
		if err := dialPeer(r.base, r.wsURL, r.gatewayURL, token, p); err != nil {
			return err
		}
	}
	room, err := r.group("e2e media", r.user, r.other)
	r.room = room
	return err
}

func (r *mediaRun) group(name string, members ...string) (string, error) {
	resp, err := viaGateway(r.a.gw, r.a.gw.chat.CreateRoom, &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: name, Members: members, RequestId: randomCID(),
	})
	if err != nil {
		return "", fmt.Errorf("create room %q: %w", name, err)
	}
	if resp.GetRoom().GetId() == "" {
		return "", errors.New("create room returned no room id")
	}
	return resp.GetRoom().GetId(), nil
}

func (r *mediaRun) sendFiles() error {
	resp, err := r.a.call(sendRequest(&chatimv1.SendMessageRequest{
		RoomId: r.room, Cid: randomCID(), Attachments: []*chatimv1.Attachment{r.image.attachment(), r.page.attachment()},
	}))
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if r.seq = resp.GetSendMessage().GetSeq(); r.seq == 0 {
		return errors.New("send returned no seq")
	}
	if _, err := r.b.await(0, e2e.CreatedMatch(r.room, r.seq)); err != nil {
		return fmt.Errorf("msg_created to B over the websocket: %w", err)
	}
	return nil
}

func (r *mediaRun) readFiles() error {
	resp, err := viaGateway(r.b.gw, r.b.gw.chat.GetMessages, &chatimv1.GetMessagesRequest{RoomId: r.room, Seqs: []uint64{r.seq}})
	if err != nil {
		return err
	}
	if len(resp.GetMessages()) != 1 {
		return fmt.Errorf("GetMessages returned %d messages, want 1", len(resp.GetMessages()))
	}
	m := resp.GetMessages()[0]
	if m.GetSeq() != r.seq || m.GetSender() != r.user || m.GetText() != "" || len(m.GetAttachments()) != 2 {
		return fmt.Errorf("message %v", m)
	}
	if err := r.image.sameAttachment(m.GetAttachments()[0]); err != nil {
		return err
	}
	return r.page.sameAttachment(m.GetAttachments()[1])
}

func (r *mediaRun) previewMimes() error {
	resp, err := viaGateway(r.b.gw, r.b.gw.chat.ListMyRooms, &chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Limit: 50})
	if err != nil {
		return err
	}
	for _, item := range resp.GetItems() {
		if item.GetRoom().GetId() != r.room {
			continue
		}
		p := item.GetPreview()
		mimes := p.GetFileMimes()
		if p.GetSeq() != r.seq || p.GetText() != "" || !slices.Contains(mimes, r.image.mime) || !slices.Contains(mimes, r.page.mime) {
			return fmt.Errorf("preview %v", p)
		}
		return nil
	}
	return fmt.Errorf("room %s missing from B's room list", r.room)
}

func (r *mediaRun) hidden() error {
	if _, err := r.b.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_HideMessage{HideMessage: &chatimv1.HideMessageRequest{
		RoomId: r.room, Seq: r.seq,
	}}}); err != nil {
		return fmt.Errorf("hide: %w", err)
	}
	if err := r.urlCount(r.b.gw, "B", r.room, r.seq, 0); err != nil {
		return err
	}
	return r.urlCount(r.a.gw, "A", r.room, r.seq, 1)
}

func (r *mediaRun) forwarded() error {
	room, err := r.group("e2e media forward", r.user, r.third)
	if err != nil {
		return err
	}
	r.forwardRoom = room
	resp, err := r.a.call(sendRequest(&chatimv1.SendMessageRequest{
		RoomId: r.forwardRoom, Cid: randomCID(), ForwardFrom: &chatimv1.ForwardRef{RoomId: r.room, Seq: r.seq},
	}))
	if err != nil {
		return fmt.Errorf("forward: %w", err)
	}
	r.forwardSeq = resp.GetSendMessage().GetSeq()
	if _, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_DeleteMessage{DeleteMessage: &chatimv1.DeleteMessageRequest{
		RoomId: r.room, Seq: r.seq,
	}}}); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := r.urlCount(r.a.gw, "A", r.room, r.seq, 0); err != nil {
		return err
	}
	c, err := r.client(r.third)
	if err != nil {
		return err
	}
	files, err := r.messageFiles(c, r.forwardRoom, r.forwardSeq)
	if err != nil {
		return err
	}
	if len(files) != 2 || files[r.page.key] == nil || files[r.image.key] == nil {
		return fmt.Errorf("forward to %s gave C the files %v", r.forwardRoom, files)
	}
	if err := r.allIn(files, r.completedBucket); err != nil {
		return fmt.Errorf("forward to %s: %w", r.forwardRoom, err)
	}
	return checkDownload(r.base, r.image, files[r.image.key].GetUrl())
}
