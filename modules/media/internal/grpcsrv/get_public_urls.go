package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetPublicURLs(ctx context.Context, req *chatimv1.GetPublicURLsRequest) (*chatimv1.GetPublicURLsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.public.URLs(ctx, who, req.GetKeys())
	if err != nil {
		return nil, err
	}
	files := make([]*chatimv1.FileURL, len(got.Files))
	for i, f := range got.Files {
		files[i] = &chatimv1.FileURL{Key: f.Key, Url: f.URL, ThumbnailUrl: f.ThumbnailURL}
	}
	return &chatimv1.GetPublicURLsResponse{Files: files, ExpiresAt: timestamppb.New(got.ExpiresAt)}, nil
}

func (r Refusing) GetPublicURLs(context.Context, *chatimv1.GetPublicURLsRequest) (*chatimv1.GetPublicURLsResponse, error) {
	return nil, r.Err
}
