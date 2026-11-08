package grpcsrv_test

import (
	"encoding/base32"
	"encoding/binary"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var fileKeyCode = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

func fileKey(user string, at time.Time, seed byte) string {
	var raw [16]byte
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(at.UnixMilli()))
	copy(raw[:6], ms[2:])
	for i := 6; i < 16; i++ {
		raw[i] = seed + byte(i)
	}
	return "acme/" + user + "/" + at.UTC().Format("20060102") + "/" + fileKeyCode.EncodeToString(raw[:])
}

func photoFile(key string) *chatimv1.Attachment {
	return &chatimv1.Attachment{Key: key, Name: "ảnh.png", Mime: "image/png", Size: 2048, Dim: []int32{640, 480}}
}

func sameAttachments(got, want []*chatimv1.Attachment) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			return false
		}
	}
	return true
}

func (rg *rig) insertWithFiles(t *testing.T, room string, seq uint64, files ...domain.Attachment) {
	t.Helper()
	m := domain.Message{
		Room: roomNumber(t, room), Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText,
		Text: "f" + strconv.FormatUint(seq, 10), CID: "c-" + strconv.FormatUint(seq, 10), CreatedAt: sentAtSeq(seq), Attachments: files,
	}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
}

func TestSendMessageStoresAttachmentsWithoutText(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	sent := []*chatimv1.Attachment{photoFile(fileKey("alice", time.Now(), 1)), photoFile(fileKey("alice", time.Now(), 2))}
	sent[1].Mime, sent[1].Dim, sent[1].Dur = "Video/MP4", nil, 12000
	if _, err := rg.client.SendMessage(as(t, "acme", "alice"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-1", Attachments: sent}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	want := []*chatimv1.Attachment{proto.Clone(sent[0]).(*chatimv1.Attachment), proto.Clone(sent[1]).(*chatimv1.Attachment)}
	want[1].Mime = "video/mp4"
	got := historyOf(t, rg, room)[0]
	if got.GetText() != "" || !sameAttachments(got.GetAttachments(), want) {
		t.Fatalf("stored %v, want no text and attachments %v", got, want)
	}
	foreign := &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-2", Attachments: []*chatimv1.Attachment{photoFile(fileKey("bob", time.Now(), 3))}}
	_, err := rg.client.SendMessage(as(t, "acme", "alice"), foreign)
	expectCode(t, err, codes.InvalidArgument)
	_, err = rg.client.SendMessage(as(t, "acme", "alice"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-3"})
	expectCode(t, err, codes.InvalidArgument)
}

func TestForwardCopiesSourceAttachmentsAndDropsTheRequestOnes(t *testing.T) {
	rg := newRig(t, options{})
	src := rg.createGroup(t, "acme", "alice", "bob")
	dst := rg.createGroup(t, "acme", "alice", "bob", "carol")
	file := photoFile(fileKey("alice", time.Now(), 1))
	if _, err := rg.client.SendMessage(as(t, "acme", "alice"), &chatimv1.SendMessageRequest{RoomId: src, Cid: "c-1", Attachments: []*chatimv1.Attachment{file}}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	rg.send(t, as(t, "acme", "alice"), src, "c-2", "plain")
	own := []*chatimv1.Attachment{photoFile(fileKey("bob", time.Now(), 2))}
	first := forwardReq(dst, "c-1", src, 1)
	first.Attachments = own
	rg.forward(t, as(t, "acme", "bob"), first)
	second := forwardReq(dst, "c-2", src, 2)
	second.Attachments = own
	rg.forward(t, as(t, "acme", "bob"), second)
	msgs := historyOf(t, rg, dst)
	if msgs[0].GetText() != "" || !sameAttachments(msgs[0].GetAttachments(), []*chatimv1.Attachment{file}) {
		t.Fatalf("forward of a file = %v, want alice's file only", msgs[0])
	}
	if msgs[1].GetText() != "plain" || len(msgs[1].GetAttachments()) != 0 {
		t.Fatalf("forward of plain text = %v, want no attachments", msgs[1])
	}
}

func TestEditKeepsAttachmentsAndStillNeedsText(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	file := photoFile(fileKey("alice", time.Now(), 1))
	if _, err := rg.client.SendMessage(alice, &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-1", Attachments: []*chatimv1.Attachment{file}}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	_, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: ""})
	expectCode(t, err, codes.InvalidArgument)
	edited, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "caption"})
	if err != nil || !sameAttachments(edited.GetMessage().GetAttachments(), []*chatimv1.Attachment{file}) {
		t.Fatalf("EditMessage = %v, %v; want the caption with the file kept", edited, err)
	}
	gone, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVer: edited.GetMessage().GetVer()})
	if err != nil || gone.GetMessage().GetAttachments() != nil {
		t.Fatalf("DeleteMessage = %v, %v; want no attachments", gone, err)
	}
	if got := historyOf(t, rg, room)[0]; !got.GetDeleted() || got.GetAttachments() != nil {
		t.Fatalf("deleted history entry = %v, want no attachments", got)
	}
}

func TestHiddenAndDeletedMessagesShowNoAttachmentsOnAnyReadPath(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	file := domain.Attachment{Key: "acme/alice/20261007/x", Name: "a.png", Mime: "image/png", Size: 9}
	for seq := uint64(1); seq <= 3; seq++ {
		rg.insertWithFiles(t, room, seq, file)
	}
	rg.insertReply(t, room, 4, 1)
	rg.thread(t, room, 1, 4, 1)
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	for _, seq := range []uint64{1, 2, 3} {
		if _, err := rg.client.PinMessage(alice, &chatimv1.PinMessageRequest{RoomId: room, Seq: seq}); err != nil {
			t.Fatalf("PinMessage(%d): %v", seq, err)
		}
	}
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	files := map[uint64]int{}
	count := func(path string, m *chatimv1.Message) {
		if m.GetSeq() == 3 && len(m.GetAttachments()) != 1 {
			t.Errorf("%s: visible seq 3 = %v, want its file", path, m)
		}
		if m.GetSeq() != 3 {
			files[m.GetSeq()] += len(m.GetAttachments())
		}
	}
	history, err := rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	for _, m := range history.GetMessages() {
		count("history", m)
	}
	byKey, err := rg.client.GetMessages(bob, &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{1, 2, 3}})
	if err != nil || len(byKey.GetMessages()) != 3 {
		t.Fatalf("GetMessages = %v, %v; want three messages", byKey, err)
	}
	for _, m := range byKey.GetMessages() {
		count("get messages", m)
	}
	pins, err := rg.client.ListPins(bob, &chatimv1.ListPinsRequest{RoomId: room})
	if err != nil || len(pins.GetItems()) != 3 {
		t.Fatalf("ListPins = %v, %v; want three pins", pins, err)
	}
	for _, it := range pins.GetItems() {
		count("pins", it.GetMessage())
	}
	threads, err := rg.client.ListThreads(bob, &chatimv1.ListThreadsRequest{RoomId: room})
	if err != nil || len(threads.GetItems()) != 1 || !threads.GetItems()[0].GetMessage().GetHidden() {
		t.Fatalf("ListThreads = %v, %v; want the hidden parent", threads, err)
	}
	count("threads", threads.GetItems()[0].GetMessage())
	if files[1] != 0 || files[2] != 0 {
		t.Fatalf("hidden or deleted messages carried files %v, want none", files)
	}
}
