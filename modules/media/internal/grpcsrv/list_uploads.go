package grpcsrv

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/gallery"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListUploads(ctx context.Context, req *chatimv1.ListUploadsRequest) (*chatimv1.ListUploadsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	kinds, err := kindsOf(req.GetKinds())
	if err != nil {
		return nil, err
	}
	page, err := s.gallery.List(ctx, who, gallery.Input{Kinds: kinds, Before: req.GetBefore(), PageSize: int(req.GetPageSize())})
	if err != nil {
		return nil, err
	}
	files := make([]*chatimv1.UploadedFile, len(page.Files))
	for i, f := range page.Files {
		u := f.Upload
		files[i] = &chatimv1.UploadedFile{
			Key: u.Key, Name: u.FileName, Mime: u.ContentType, Size: u.Size, Dim: u.Dim, Dur: u.Dur, Hash: u.Hash,
			CreatedAt: timestamppb.New(u.CreatedAt), Expired: f.Expired, Url: f.URL, ThumbnailUrl: f.ThumbnailURL,
		}
	}
	return &chatimv1.ListUploadsResponse{Files: files, NextBefore: page.NextBefore, ExpiresAt: timestamppb.New(page.ExpiresAt)}, nil
}

func kindsOf(in []chatimv1.FileKind) (domain.Kinds, error) {
	var k domain.Kinds
	for _, kind := range in {
		switch kind {
		case chatimv1.FileKind_FILE_KIND_IMAGE:
			k.Image = true
		case chatimv1.FileKind_FILE_KIND_VIDEO:
			k.Video = true
		case chatimv1.FileKind_FILE_KIND_OTHER:
			k.Other = true
		default:
			return domain.Kinds{}, fmt.Errorf("%w: unknown file kind %d", domain.ErrInvalid, kind)
		}
	}
	return k, nil
}
