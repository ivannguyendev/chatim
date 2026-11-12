package grpcsrv_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestListRoomFilesOverTheService(t *testing.T) {
	s, _, pairs := newServiceWithPairs(t)
	key := "acme/an/20261107/0123456789abcdefghijklmnop"
	sent := time.Date(2026, 11, 11, 9, 1, 0, 0, time.UTC)
	ref := domain.MessageRef{Room: "7", Seq: 9}
	pair := domain.MessageFile{MessageKey: ref.Key(), FileKey: key, Tenant: "acme", Room: "7", Kind: domain.RoomFileMedia, SentAt: sent}
	if err := pairs.Attach(t.Context(), []domain.MessageFile{pair}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListRoomFiles(caller("acme", "binh"), &chatimv1.ListRoomFilesRequest{RoomId: "7", Kind: chatimv1.RoomFileKind_ROOM_FILE_KIND_MEDIA, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := &chatimv1.RoomFile{
		Message: &chatimv1.MessageRef{RoomId: "7", Seq: 9},
		File:    &chatimv1.Attachment{Key: key, Name: "a.png", Mime: "image/png"},
		Url:     "https://s3/" + key,
	}
	if len(got.GetFiles()) != 1 || got.GetNextBefore() != "" || !got.GetExpiresAt().IsValid() {
		t.Fatalf("got %v", got)
	}
	f := got.GetFiles()[0]
	if !f.GetSentAt().IsValid() {
		t.Fatalf("no send time in %v", f)
	}
	f.SentAt = nil
	if !proto.Equal(f, want) {
		t.Fatalf("file %v, want %v", f, want)
	}
	other, err := s.ListRoomFiles(caller("acme", "binh"), &chatimv1.ListRoomFilesRequest{RoomId: "7", Kind: chatimv1.RoomFileKind_ROOM_FILE_KIND_FILE, PageSize: 10})
	if err != nil || len(other.GetFiles()) != 0 {
		t.Fatalf("file tab %v, %v", other, err)
	}
	if _, err := s.ListRoomFiles(caller("acme", "an"), &chatimv1.ListRoomFilesRequest{RoomId: "7", Kind: chatimv1.RoomFileKind_ROOM_FILE_KIND_MEDIA, PageSize: 10}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("outsider err = %v", err)
	}
	for _, kind := range []chatimv1.RoomFileKind{chatimv1.RoomFileKind_ROOM_FILE_KIND_UNSPECIFIED, chatimv1.RoomFileKind(9)} {
		if _, err := s.ListRoomFiles(caller("acme", "binh"), &chatimv1.ListRoomFilesRequest{RoomId: "7", Kind: kind, PageSize: 10}); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("kind %v err = %v", kind, err)
		}
	}
}
