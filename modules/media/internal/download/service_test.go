package download_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var (
	now = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	an  = domain.Caller{Tenant: "acme", User: "an"}
)

const (
	photo  = "acme/binh/20261107/0123456789abcdefghijklmnop"
	clip   = "acme/binh/20261107/0123456789abcdefghijklmnoq"
	report = "acme/binh/20261107/0123456789abcdefghijklmnor"
)

type fakeChat struct {
	asked [][]domain.MessageRef
	reply []domain.MessageFiles
	err   error
}

func (f *fakeChat) VisibleAttachments(_ context.Context, tenant, user string, refs []domain.MessageRef) ([]domain.MessageFiles, error) {
	if tenant != an.Tenant || user != an.User {
		return nil, fmt.Errorf("asked as %s/%s", tenant, user)
	}
	f.asked = append(f.asked, refs)
	return f.reply, f.err
}

type signedGet struct {
	bucket domain.Bucket
	key    string
	ttl    time.Duration
}

type getMany struct {
	tenant string
	keys   []string
}

type fakeUploads struct {
	asked []getMany
	docs  map[string]domain.Upload
	err   error
}

func (f *fakeUploads) GetMany(_ context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	f.asked = append(f.asked, getMany{tenant: tenant, keys: slices.Clone(keys)})
	return maps.Clone(f.docs), f.err
}

type fakeObjects struct {
	mu     sync.Mutex
	signed []signedGet
	err    error
}

func (f *fakeObjects) SignGet(_ context.Context, bucket domain.Bucket, key string, ttl time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signed = append(f.signed, signedGet{bucket: bucket, key: key, ttl: ttl})
	return "https://s3/" + string(bucket) + "/" + key, f.err
}

type rig struct {
	svc     *download.Service
	chat    *fakeChat
	uploads *fakeUploads
	objects *fakeObjects
	ready   bool
}

func newRig(t *testing.T, thumbs domain.Thumbnails) *rig {
	t.Helper()
	r := &rig{chat: &fakeChat{}, uploads: &fakeUploads{}, objects: &fakeObjects{}, ready: true}
	svc, err := download.New(download.Config{SignatureTTL: time.Hour, Thumbnails: thumbs}, download.Deps{
		Chat: r.chat, Uploads: r.uploads, Objects: r.objects, Ready: func() bool { return r.ready }, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func refs(n int) []domain.MessageRef {
	out := make([]domain.MessageRef, n)
	for i := range out {
		out[i] = domain.MessageRef{Room: "7", Seq: uint64(i + 1)}
	}
	return out
}

func filesMessage(seq uint64, files ...domain.File) domain.MessageFiles {
	return domain.MessageFiles{Ref: domain.MessageRef{Room: "7", Seq: seq}, Files: files}
}

func inProcessing(key string) string { return "https://s3/processing/" + key }

func inCompleted(key string) string { return "https://s3/completed/" + key }

func TestNewNeedsChatUploadsObjectsAndATTL(t *testing.T) {
	if _, err := download.New(download.Config{SignatureTTL: time.Hour}, download.Deps{Chat: &fakeChat{}, Uploads: &fakeUploads{}}); err == nil {
		t.Fatal("missing objects accepted")
	}
	if _, err := download.New(download.Config{SignatureTTL: time.Hour}, download.Deps{Chat: &fakeChat{}, Objects: &fakeObjects{}}); err == nil {
		t.Fatal("missing uploads accepted")
	}
	if _, err := download.New(download.Config{}, download.Deps{Chat: &fakeChat{}, Uploads: &fakeUploads{}, Objects: &fakeObjects{}}); err == nil {
		t.Fatal("zero ttl accepted")
	}
}

func TestAttachmentURLsSignEveryVisibleFile(t *testing.T) {
	r := newRig(t, domain.Thumbnails{Suffix: ".thumb"})
	r.chat.reply = []domain.MessageFiles{
		filesMessage(2, domain.File{Key: photo, Name: "a.png", Mime: "image/png"}, domain.File{Key: report, Name: "r.pdf", Mime: "application/pdf"}),
		filesMessage(1, domain.File{Key: clip, Name: "c.mp4", Mime: "video/mp4"}),
	}
	got, err := r.svc.AttachmentURLs(t.Context(), an, refs(3))
	if err != nil {
		t.Fatal(err)
	}
	if !got.ExpiresAt.Equal(now.Add(time.Hour)) || len(got.Messages) != 2 || got.Messages[0].Ref.Seq != 2 || got.Messages[1].Ref.Seq != 1 {
		t.Fatalf("result %+v", got)
	}
	first := got.Messages[0].Files
	if len(first) != 2 || first[0] != (download.FileURL{Key: photo, URL: inProcessing(photo)}) || first[1].URL != inProcessing(report) || first[1].ThumbnailURL != "" {
		t.Fatalf("files %+v", first)
	}
	want := []signedGet{
		{domain.BucketProcessing, photo, time.Hour},
		{domain.BucketProcessing, report, time.Hour},
		{domain.BucketProcessing, clip, time.Hour},
	}
	if !slices.Equal(r.objects.signed, want) {
		t.Fatalf("signed %+v", r.objects.signed)
	}
}

func TestThumbnailURLsFollowTheConfiguredSuffixForImagesAndVideos(t *testing.T) {
	r := newRig(t, domain.Thumbnails{Enabled: true, Suffix: ".small"})
	r.chat.reply = []domain.MessageFiles{filesMessage(1,
		domain.File{Key: photo, Name: "a.png", Mime: "image/png"},
		domain.File{Key: clip, Name: "c.mp4", Mime: "video/mp4"},
		domain.File{Key: report, Name: "r.pdf", Mime: "application/pdf"},
	)}
	got, err := r.svc.AttachmentURLs(t.Context(), an, refs(1))
	if err != nil {
		t.Fatal(err)
	}
	files := got.Messages[0].Files
	if files[0].ThumbnailURL != inProcessing(photo+".small") || files[1].ThumbnailURL != inProcessing(clip+".small") || files[2].ThumbnailURL != "" {
		t.Fatalf("thumbnails %+v", files)
	}
	thumbs := 0
	for _, s := range r.objects.signed {
		if strings.HasSuffix(s.key, ".small") {
			thumbs++
		}
	}
	if thumbs != 2 || len(r.objects.signed) != 5 {
		t.Fatalf("signed %+v", r.objects.signed)
	}
}

func TestAttachmentURLsAskForOneToAHundredMessages(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	for _, n := range []int{0, download.MaxMessages + 1} {
		if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(n)); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%d refs: err = %v", n, err)
		}
	}
	if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(download.MaxMessages)); err != nil {
		t.Fatalf("%d refs: %v", download.MaxMessages, err)
	}
	if len(r.chat.asked) != 1 {
		t.Fatalf("chat asked %d times", len(r.chat.asked))
	}
}

func TestDuplicateRefsAreAskedOnce(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	in := []domain.MessageRef{{Room: "7", Seq: 2}, {Room: "7", Seq: 1}, {Room: "7", Seq: 2}, {Room: "7", Thread: 2, Seq: 1}}
	if _, err := r.svc.AttachmentURLs(t.Context(), an, in); err != nil {
		t.Fatal(err)
	}
	want := []domain.MessageRef{{Room: "7", Seq: 2}, {Room: "7", Seq: 1}, {Room: "7", Thread: 2, Seq: 1}}
	if !slices.Equal(r.chat.asked[0], want) {
		t.Fatalf("asked %v", r.chat.asked[0])
	}
}

func TestAttachmentURLsRefuseUntilStorageIsReady(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.ready = false
	if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(1)); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(r.chat.asked) != 0 {
		t.Fatal("chat asked while not ready")
	}
}

func TestChatAndSigningErrorsComeBack(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.chat.err = fmt.Errorf("room id: %w", apperr.ErrInvalidArgument)
	if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(1)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("chat err = %v", err)
	}
	r.chat.err = nil
	r.chat.reply = []domain.MessageFiles{filesMessage(1, domain.File{Key: photo, Name: "a.png", Mime: "image/png"})}
	r.objects.err = errors.New("no credentials")
	if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(1)); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("sign err = %v", err)
	}
}
