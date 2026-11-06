package grpcsrv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
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

func caller(tenant, user string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(chatrpc.TenantHeader, tenant, chatrpc.UserHeader, user))
}

func newService(t *testing.T) *grpcsrv.Service {
	t.Helper()
	svc, err := upload.New(upload.Config{
		Limits:     domain.Limits{MinPartBytes: 16 << 20, MaxPartBytes: 128 << 20, MaxParts: 2000, MaxParallel: 4, MaxFileBytes: 4 << 30},
		PartURLTTL: time.Minute,
	}, upload.Deps{Uploads: memstore.NewUploads(), Objects: objects{completed: map[string]bool{}}})
	if err != nil {
		t.Fatal(err)
	}
	return grpcsrv.New(svc)
}

func TestUploadRoundTripOverTheService(t *testing.T) {
	s := newService(t)
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

func TestCallsWithoutACallerAreRefused(t *testing.T) {
	s := newService(t)
	if _, err := s.StartUpload(context.Background(), &chatimv1.StartUploadRequest{FileName: "a", ContentType: "a/b", Size: 1}); err == nil {
		t.Fatal("no caller accepted")
	}
}

func TestDisabledRefusesEveryCall(t *testing.T) {
	var d grpcsrv.Disabled
	ctx := caller("acme", "an")
	_, e1 := d.StartUpload(ctx, &chatimv1.StartUploadRequest{})
	_, e2 := d.GetPartURLs(ctx, &chatimv1.GetPartURLsRequest{})
	_, e3 := d.CompleteUpload(ctx, &chatimv1.CompleteUploadRequest{})
	_, e4 := d.AbortUpload(ctx, &chatimv1.AbortUploadRequest{})
	for _, err := range []error{e1, e2, e3, e4} {
		if !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("err = %v", err)
		}
	}
}
