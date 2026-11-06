package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Service struct {
	chatimv1.UnimplementedMediaServiceServer
	uploads *upload.Service
}

func New(uploads *upload.Service) *Service { return &Service{uploads: uploads} }

func callerOf(ctx context.Context) (upload.Caller, error) {
	who, err := chatrpc.CallerOf(ctx)
	if err != nil {
		return upload.Caller{}, err
	}
	return upload.Caller{Tenant: who.Tenant, User: who.User}, nil
}

func (s *Service) StartUpload(ctx context.Context, req *chatimv1.StartUploadRequest) (*chatimv1.StartUploadResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	started, err := s.uploads.Start(ctx, who, upload.StartInput{
		FileName: req.GetFileName(), ContentType: req.GetContentType(), Size: req.GetSize(), BytesPerSecond: req.GetUploadBytesPerSecond(),
	})
	if err != nil {
		return nil, err
	}
	parts := make([]*chatimv1.UploadPart, len(started.Parts))
	for i, p := range started.Parts {
		parts[i] = &chatimv1.UploadPart{PartNumber: p.Number, Size: p.Size}
	}
	return &chatimv1.StartUploadResponse{
		Key: started.Upload.Key, PartSize: started.Upload.PartSize, Parts: parts,
		Urls: partURLs(started.URLs), ExpiresAt: timestamppb.New(started.ExpiresAt),
	}, nil
}

func (s *Service) GetPartURLs(ctx context.Context, req *chatimv1.GetPartURLsRequest) (*chatimv1.GetPartURLsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	urls, expires, err := s.uploads.PartURLs(ctx, who, req.GetKey(), req.GetPartNumbers())
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetPartURLsResponse{Urls: partURLs(urls), ExpiresAt: timestamppb.New(expires)}, nil
}

func (s *Service) CompleteUpload(ctx context.Context, req *chatimv1.CompleteUploadRequest) (*chatimv1.CompleteUploadResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	parts := make([]domain.UploadedPart, len(req.GetParts()))
	for i, p := range req.GetParts() {
		parts[i] = domain.UploadedPart{Number: p.GetPartNumber(), ETag: p.GetEtag()}
	}
	u, err := s.uploads.Complete(ctx, who, req.GetKey(), parts)
	if err != nil {
		return nil, err
	}
	return &chatimv1.CompleteUploadResponse{Key: u.Key, Size: u.Size}, nil
}

func (s *Service) AbortUpload(ctx context.Context, req *chatimv1.AbortUploadRequest) (*chatimv1.AbortUploadResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uploads.Abort(ctx, who, req.GetKey()); err != nil {
		return nil, err
	}
	return &chatimv1.AbortUploadResponse{}, nil
}

func partURLs(in []upload.PartURL) []*chatimv1.PartURL {
	out := make([]*chatimv1.PartURL, len(in))
	for i, u := range in {
		out[i] = &chatimv1.PartURL{PartNumber: u.Number, Url: u.URL}
	}
	return out
}

type Disabled struct {
	chatimv1.UnimplementedMediaServiceServer
}

func (Disabled) StartUpload(context.Context, *chatimv1.StartUploadRequest) (*chatimv1.StartUploadResponse, error) {
	return nil, domain.ErrDisabled
}

func (Disabled) GetPartURLs(context.Context, *chatimv1.GetPartURLsRequest) (*chatimv1.GetPartURLsResponse, error) {
	return nil, domain.ErrDisabled
}

func (Disabled) CompleteUpload(context.Context, *chatimv1.CompleteUploadRequest) (*chatimv1.CompleteUploadResponse, error) {
	return nil, domain.ErrDisabled
}

func (Disabled) AbortUpload(context.Context, *chatimv1.AbortUploadRequest) (*chatimv1.AbortUploadResponse, error) {
	return nil, domain.ErrDisabled
}
