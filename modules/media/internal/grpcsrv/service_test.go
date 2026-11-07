package grpcsrv_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
	"github.com/ivannguyendev/chatim/modules/media/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type objects struct{ completed map[string]bool }

func (objects) StartMultipart(context.Context, string, string) (string, error) { return "id", nil }
func (objects) SignPart(_ context.Context, key, _ string, n int32, _ int64, _ time.Duration) (string, error) {
	return "https://s3/" + key, nil
}
func (o objects) Complete(_ context.Context, key, _ string, _ []domain.UploadedPart) error {
	o.completed[key] = true
	return nil
}
func (objects) Abort(context.Context, string, string) error { return nil }
func (objects) Head(context.Context, string) (int64, error) { return 40 << 20, nil }
func (objects) Delete(context.Context, string) error        { return nil }
func (objects) SignGet(_ context.Context, key string, _ time.Duration, d domain.Disposition) (string, error) {
	return "https://s3/" + key + "?" + d.Disposition, nil
}

type chat struct{}

func (chat) VisibleAttachments(_ context.Context, _, user string, refs []domain.MessageRef) ([]domain.MessageFiles, error) {
	if user != "binh" {
		return nil, nil
	}
	return []domain.MessageFiles{{Ref: refs[0], Files: []domain.File{{Key: "acme/an/20261107/0123456789abcdefghijklmnop", Name: "a.png", Mime: "image/png"}}}}, nil
}

func caller(tenant, user string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(chatrpc.TenantHeader, tenant, chatrpc.UserHeader, user))
}

func newService(t *testing.T) (*grpcsrv.Service, *memstore.Uploads) {
	t.Helper()
	uploads, store := memstore.NewUploads(), objects{completed: map[string]bool{}}
	svc, err := upload.New(upload.Config{
		Limits:     domain.Limits{MinPartBytes: 16 << 20, MaxPartBytes: 128 << 20, MaxParts: 2000, MaxParallel: 4, MaxFileBytes: 4 << 30},
		PartURLTTL: time.Minute,
	}, upload.Deps{Uploads: uploads, Objects: store})
	if err != nil {
		t.Fatal(err)
	}
	downloads, err := download.New(download.Config{SignatureTTL: time.Hour, Thumbnails: domain.Thumbnails{Enabled: true, Suffix: ".thumb"}}, download.Deps{Chat: chat{}, Objects: store})
	if err != nil {
		t.Fatal(err)
	}
	return grpcsrv.New(svc, downloads), uploads
}

func TestGetAttachmentURLsOverTheService(t *testing.T) {
	s, _ := newService(t)
	req := &chatimv1.GetAttachmentURLsRequest{Messages: []*chatimv1.MessageRef{{RoomId: "7", Thread: 3, Seq: 9}}}
	got, err := s.GetAttachmentURLs(caller("acme", "binh"), req)
	if err != nil {
		t.Fatal(err)
	}
	key := "acme/an/20261107/0123456789abcdefghijklmnop"
	want := &chatimv1.MessageFiles{RoomId: "7", Thread: 3, Seq: 9, Files: []*chatimv1.FileURL{{Key: key, Url: "https://s3/" + key + "?inline", ThumbnailUrl: "https://s3/" + key + ".thumb?inline"}}}
	if len(got.GetMessages()) != 1 || !proto.Equal(got.GetMessages()[0], want) || !got.GetExpiresAt().IsValid() {
		t.Fatalf("got %v", got)
	}
	if none, err := s.GetAttachmentURLs(caller("acme", "an"), req); err != nil || len(none.GetMessages()) != 0 {
		t.Fatalf("other reader got %v, %v", none, err)
	}
	if _, err := s.GetAttachmentURLs(caller("acme", "binh"), &chatimv1.GetAttachmentURLsRequest{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("no refs err = %v", err)
	}
}

func TestUploadRoundTripOverTheService(t *testing.T) {
	s, _ := newService(t)
	ctx := caller("acme", "an")
	started, err := s.StartUpload(ctx, &chatimv1.StartUploadRequest{FileName: "clip.mp4", ContentType: "video/mp4", Size: 40 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(started.GetParts()) != 3 || len(started.GetUrls()) != 3 || started.GetParts()[2].GetSize() != 8<<20 || !started.GetExpiresAt().IsValid() {
		t.Fatalf("started %v", started)
	}
	urls, err := s.GetPartURLs(ctx, &chatimv1.GetPartURLsRequest{Key: started.GetKey(), PartNumbers: []int32{2}})
	if err != nil || len(urls.GetUrls()) != 1 || urls.GetUrls()[0].GetPartNumber() != 2 {
		t.Fatalf("urls %v, %v", urls, err)
	}
	parts := []*chatimv1.UploadedPart{{PartNumber: 1, Etag: "a"}, {PartNumber: 2, Etag: "b"}, {PartNumber: 3, Etag: "c"}}
	done, err := s.CompleteUpload(ctx, &chatimv1.CompleteUploadRequest{Key: started.GetKey(), Parts: parts})
	if err != nil || done.GetSize() != 40<<20 || done.GetKey() != started.GetKey() {
		t.Fatalf("done %v, %v", done, err)
	}
	if _, err := s.AbortUpload(caller("acme", "binh"), &chatimv1.AbortUploadRequest{Key: started.GetKey()}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("other user's abort: %v", err)
	}
}

func TestFileFactsReachTheUploadDoc(t *testing.T) {
	s, uploads := newService(t)
	ctx := caller("acme", "an")
	started, err := s.StartUpload(ctx, &chatimv1.StartUploadRequest{
		FileName: "clip.mp4", ContentType: "Video/MP4", Size: 40 << 20, Dim: []int32{1920, 1080}, Dur: 95400,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := "sha256:" + strings.Repeat("9f86d081", 8)
	parts := []*chatimv1.UploadedPart{{PartNumber: 1, Etag: "a"}, {PartNumber: 2, Etag: "b"}, {PartNumber: 3, Etag: "c"}}
	if _, err := s.CompleteUpload(ctx, &chatimv1.CompleteUploadRequest{Key: started.GetKey(), Parts: parts, Hash: hash}); err != nil {
		t.Fatal(err)
	}
	got, err := uploads.Get(ctx, started.GetKey())
	if err != nil || !slices.Equal(got.Dim, []int32{1920, 1080}) || got.Dur != 95400 || got.Hash != hash || got.ContentType != "video/mp4" {
		t.Fatalf("stored %+v, %v", got, err)
	}
	if _, err := s.CompleteUpload(ctx, &chatimv1.CompleteUploadRequest{Key: started.GetKey(), Parts: parts, Hash: "sha256:bad"}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("bad hash err = %v", err)
	}
}

func TestCallsWithoutACallerAreRefused(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.StartUpload(context.Background(), &chatimv1.StartUploadRequest{FileName: "a", ContentType: "a/b", Size: 1}); err == nil {
		t.Fatal("no caller accepted")
	}
}

func TestRefusingAnswersEveryCallWithItsError(t *testing.T) {
	d := grpcsrv.Refusing{Err: domain.ErrDisabled}
	ctx := caller("acme", "an")
	_, e1 := d.StartUpload(ctx, &chatimv1.StartUploadRequest{})
	_, e2 := d.GetPartURLs(ctx, &chatimv1.GetPartURLsRequest{})
	_, e3 := d.CompleteUpload(ctx, &chatimv1.CompleteUploadRequest{})
	_, e4 := d.AbortUpload(ctx, &chatimv1.AbortUploadRequest{})
	_, e5 := d.GetAttachmentURLs(ctx, &chatimv1.GetAttachmentURLsRequest{})
	_, e6 := d.ListUploads(ctx, &chatimv1.ListUploadsRequest{})
	for _, err := range []error{e1, e2, e3, e4, e5, e6} {
		if !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("err = %v", err)
		}
	}
}
