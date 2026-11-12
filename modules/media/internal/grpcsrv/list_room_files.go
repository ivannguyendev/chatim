package grpcsrv

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/roomfiles"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListRoomFiles(ctx context.Context, req *chatimv1.ListRoomFilesRequest) (*chatimv1.ListRoomFilesResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	kind, err := roomFileKindOf(req.GetKind())
	if err != nil {
		return nil, err
	}
	page, err := s.rooms.List(ctx, who, roomfiles.Input{Room: req.GetRoomId(), Kind: kind, Before: req.GetBefore(), PageSize: int(req.GetPageSize())})
	if err != nil {
		return nil, err
	}
	files := make([]*chatimv1.RoomFile, len(page.Files))
	for i, f := range page.Files {
		files[i] = &chatimv1.RoomFile{
			Message:  &chatimv1.MessageRef{RoomId: f.Ref.Room, Thread: f.Ref.Thread, Seq: f.Ref.Seq},
			SenderId: f.Sender, SentAt: timestamppb.New(f.SentAt), File: attachmentOf(f.File),
			Url: f.URL, ThumbnailUrl: f.ThumbnailURL,
		}
	}
	return &chatimv1.ListRoomFilesResponse{Files: files, NextBefore: page.NextBefore, ExpiresAt: timestamppb.New(page.ExpiresAt)}, nil
}

func roomFileKindOf(kind chatimv1.RoomFileKind) (domain.RoomFileKind, error) {
	switch kind {
	case chatimv1.RoomFileKind_ROOM_FILE_KIND_MEDIA:
		return domain.RoomFileMedia, nil
	case chatimv1.RoomFileKind_ROOM_FILE_KIND_FILE:
		return domain.RoomFileOther, nil
	case chatimv1.RoomFileKind_ROOM_FILE_KIND_UNSPECIFIED:
	}
	return "", fmt.Errorf("%w: unknown room file kind %d", domain.ErrInvalid, kind)
}

func attachmentOf(f domain.File) *chatimv1.Attachment {
	return &chatimv1.Attachment{Key: f.Key, Name: f.Name, Mime: f.Mime, Size: f.Size, Dim: f.Dim, Dur: f.Dur, Hash: f.Hash}
}

func (r Refusing) ListRoomFiles(context.Context, *chatimv1.ListRoomFilesRequest) (*chatimv1.ListRoomFilesResponse, error) {
	return nil, r.Err
}
