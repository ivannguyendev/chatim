package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Service struct {
	chatimv1.UnimplementedMediaServiceServer
	uploads   *upload.Service
	downloads *download.Service
}

func New(uploads *upload.Service, downloads *download.Service) *Service {
	return &Service{uploads: uploads, downloads: downloads}
}

func callerOf(ctx context.Context) (domain.Caller, error) {
	who, err := chatrpc.CallerOf(ctx)
	if err != nil {
		return domain.Caller{}, err
	}
	return domain.Caller{Tenant: who.Tenant, User: who.User}, nil
}

func (s *Service) StartUpload(ctx context.Context, req *chatimv1.StartUploadRequest) (*chatimv1.StartUploadResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	started, err := s.uploads.Start(ctx, who, upload.StartInput{
		FileName: req.GetFileName(), ContentType: req.GetContentType(), Size: req.GetSize(),
		Dim: req.GetDim(), Dur: req.GetDur(), BytesPerSecond: req.GetUploadBytesPerSecond(),
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
	u, err := s.uploads.Complete(ctx, who, req.GetKey(), parts, req.GetHash())
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

func (s *Service) GetAttachmentURLs(ctx context.Context, req *chatimv1.GetAttachmentURLsRequest) (*chatimv1.GetAttachmentURLsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]domain.MessageRef, len(req.GetMessages()))
	for i, m := range req.GetMessages() {
		refs[i] = domain.MessageRef{Room: m.GetRoomId(), Thread: m.GetThread(), Seq: m.GetSeq()}
	}
	got, err := s.downloads.AttachmentURLs(ctx, who, refs)
	if err != nil {
		return nil, err
	}
	out := make([]*chatimv1.MessageFiles, len(got.Messages))
	for i, m := range got.Messages {
		files := make([]*chatimv1.FileURL, len(m.Files))
		for j, f := range m.Files {
			files[j] = &chatimv1.FileURL{Key: f.Key, Url: f.URL, ThumbnailUrl: f.ThumbnailURL}
		}
		out[i] = &chatimv1.MessageFiles{RoomId: m.Ref.Room, Thread: m.Ref.Thread, Seq: m.Ref.Seq, Files: files}
	}
	return &chatimv1.GetAttachmentURLsResponse{Messages: out, ExpiresAt: timestamppb.New(got.ExpiresAt)}, nil
}

func partURLs(in []upload.PartURL) []*chatimv1.PartURL {
	out := make([]*chatimv1.PartURL, len(in))
	for i, u := range in {
		out[i] = &chatimv1.PartURL{PartNumber: u.Number, Url: u.URL}
	}
	return out
}

type Refusing struct {
	chatimv1.UnimplementedMediaServiceServer
	Err error
}

func (r Refusing) StartUpload(context.Context, *chatimv1.StartUploadRequest) (*chatimv1.StartUploadResponse, error) {
	return nil, r.Err
}

func (r Refusing) GetPartURLs(context.Context, *chatimv1.GetPartURLsRequest) (*chatimv1.GetPartURLsResponse, error) {
	return nil, r.Err
}

func (r Refusing) CompleteUpload(context.Context, *chatimv1.CompleteUploadRequest) (*chatimv1.CompleteUploadResponse, error) {
	return nil, r.Err
}

func (r Refusing) AbortUpload(context.Context, *chatimv1.AbortUploadRequest) (*chatimv1.AbortUploadResponse, error) {
	return nil, r.Err
}

func (r Refusing) GetAttachmentURLs(context.Context, *chatimv1.GetAttachmentURLsRequest) (*chatimv1.GetAttachmentURLsResponse, error) {
	return nil, r.Err
}

func (r Refusing) ListUploads(context.Context, *chatimv1.ListUploadsRequest) (*chatimv1.ListUploadsResponse, error) {
	return nil, r.Err
}
