package download_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
)

func completedUpload(tenant, key string) domain.Upload {
	return domain.Upload{Key: key, Tenant: tenant, State: domain.StateUploaded, Bucket: domain.BucketCompleted}
}

func manyFiles(n int) []domain.File {
	out := make([]domain.File, n)
	for i := range out {
		out[i] = domain.File{Key: fmt.Sprintf("acme/binh/20261107/%026d", i), Name: "f", Mime: "application/pdf"}
	}
	return out
}

func TestCompletedFilesAndTheirThumbnailsAreSignedFromCompleted(t *testing.T) {
	r := newRig(t, domain.Thumbnails{Enabled: true, Suffix: ".thumb"})
	r.uploads.docs = map[string]domain.Upload{photo: completedUpload("acme", photo)}
	r.chat.reply = []domain.MessageFiles{filesMessage(1,
		domain.File{Key: photo, Name: "a.png", Mime: "image/png"},
		domain.File{Key: clip, Name: "c.mp4", Mime: "video/mp4"},
	)}
	got, err := r.svc.AttachmentURLs(t.Context(), an, refs(1))
	if err != nil {
		t.Fatal(err)
	}
	files := got.Messages[0].Files
	wantFiles := []download.FileURL{
		{Key: photo, URL: inCompleted(photo), ThumbnailURL: inCompleted(photo + ".thumb")},
		{Key: clip, URL: inProcessing(clip), ThumbnailURL: inProcessing(clip + ".thumb")},
	}
	if !slices.Equal(files, wantFiles) {
		t.Fatalf("files %+v", files)
	}
	want := []getMany{{tenant: an.Tenant, keys: []string{photo, clip}}}
	if !slices.EqualFunc(r.uploads.asked, want, sameGetMany) {
		t.Fatalf("asked %+v", r.uploads.asked)
	}
}

func TestUploadsAreReadOncePerTwoHundredKeys(t *testing.T) {
	for _, n := range []int{0, 1, download.UploadBatch, download.UploadBatch + 1, 2*download.UploadBatch + 1} {
		r := newRig(t, domain.Thumbnails{})
		files := manyFiles(n)
		r.chat.reply = []domain.MessageFiles{filesMessage(1, files...)}
		if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(1)); err != nil {
			t.Fatal(err)
		}
		wantCalls := (n + download.UploadBatch - 1) / download.UploadBatch
		if len(r.uploads.asked) != wantCalls {
			t.Fatalf("%d keys: GetMany called %d times, want %d", n, len(r.uploads.asked), wantCalls)
		}
		var asked []string
		for _, call := range r.uploads.asked {
			if call.tenant != an.Tenant || len(call.keys) > download.UploadBatch {
				t.Fatalf("%d keys: call %s with %d keys", n, call.tenant, len(call.keys))
			}
			asked = append(asked, call.keys...)
		}
		wantKeys := make([]string, n)
		for i, f := range files {
			wantKeys[i] = f.Key
		}
		if !slices.Equal(asked, wantKeys) || len(r.objects.signed) != n {
			t.Fatalf("%d keys: asked %d, signed %d", n, len(asked), len(r.objects.signed))
		}
	}
}

func TestAFileSharedByMessagesIsLookedUpOnce(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.uploads.docs = map[string]domain.Upload{photo: completedUpload("acme", photo)}
	shared := domain.File{Key: photo, Name: "a.png", Mime: "image/png"}
	r.chat.reply = []domain.MessageFiles{filesMessage(2, shared), filesMessage(1, shared)}
	got, err := r.svc.AttachmentURLs(t.Context(), an, refs(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.uploads.asked) != 1 || !slices.Equal(r.uploads.asked[0].keys, []string{photo}) {
		t.Fatalf("asked %+v", r.uploads.asked)
	}
	if got.Messages[0].Files[0].URL != inCompleted(photo) || got.Messages[1].Files[0].URL != inCompleted(photo) {
		t.Fatalf("result %+v", got.Messages)
	}
}

func TestAnUploadOfAnotherTenantIsNotUsed(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.uploads.docs = map[string]domain.Upload{photo: completedUpload("other", photo)}
	r.chat.reply = []domain.MessageFiles{filesMessage(1, domain.File{Key: photo, Name: "a.png", Mime: "image/png"})}
	got, err := r.svc.AttachmentURLs(t.Context(), an, refs(1))
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages[0].Files[0].URL != inProcessing(photo) {
		t.Fatalf("file %+v", got.Messages[0].Files[0])
	}
	if len(r.uploads.asked) != 1 || r.uploads.asked[0].tenant != an.Tenant {
		t.Fatalf("asked %+v", r.uploads.asked)
	}
}

func TestOnlyFilesChatReturnedAreLookedUpAndSigned(t *testing.T) {
	r := newRig(t, domain.Thumbnails{Enabled: true, Suffix: ".thumb"})
	r.uploads.docs = map[string]domain.Upload{
		photo: completedUpload("acme", photo), clip: completedUpload("acme", clip), report: completedUpload("acme", report),
	}
	r.chat.reply = []domain.MessageFiles{filesMessage(1, domain.File{Key: report, Name: "r.pdf", Mime: "application/pdf"})}
	got, err := r.svc.AttachmentURLs(t.Context(), an, refs(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.uploads.asked) != 1 || !slices.Equal(r.uploads.asked[0].keys, []string{report}) {
		t.Fatalf("asked %+v", r.uploads.asked)
	}
	want := []signedGet{{domain.BucketCompleted, report, time.Hour}}
	if !slices.Equal(r.objects.signed, want) || len(got.Messages) != 1 || len(got.Messages[0].Files) != 1 {
		t.Fatalf("signed %+v, result %+v", r.objects.signed, got.Messages)
	}
}

func TestUploadReadErrorsComeBackWithoutSigning(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.uploads.err = errors.New("mongo down")
	r.chat.reply = []domain.MessageFiles{filesMessage(1, domain.File{Key: photo, Name: "a.png", Mime: "image/png"})}
	if _, err := r.svc.AttachmentURLs(t.Context(), an, refs(1)); !errors.Is(err, r.uploads.err) {
		t.Fatalf("err = %v", err)
	}
	if len(r.objects.signed) != 0 {
		t.Fatalf("signed %+v", r.objects.signed)
	}
}

func sameGetMany(a, b getMany) bool { return a.tenant == b.tenant && slices.Equal(a.keys, b.keys) }
