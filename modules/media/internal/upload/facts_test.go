package upload_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var (
	firstHash  = "sha256:" + strings.Repeat("9f86d081", 8)
	secondHash = "sha256:" + strings.Repeat("0123abcd", 8)
)

func TestStartStoresFileFactsAndLowercaseContentType(t *testing.T) {
	r := newRig(t)
	s, err := r.svc.Start(t.Context(), an, upload.StartInput{
		FileName: "photo.png", ContentType: "Image/PNG", Size: 40 * mib, Dim: []int32{1920, 1080}, Dur: 95400,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := r.uploads.Get(t.Context(), s.Upload.Key)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContentType != "image/png" || !slices.Equal(stored.Dim, []int32{1920, 1080}) || stored.Dur != 95400 {
		t.Fatalf("stored %+v", stored)
	}
	if r.objects.contentType != "image/png" {
		t.Fatalf("storage content type = %q", r.objects.contentType)
	}
}

func TestStartRefusesBadFileFacts(t *testing.T) {
	cases := map[string]upload.StartInput{
		"dim with one value": {Dim: []int32{1920}},
		"dim with a zero":    {Dim: []int32{0, 1080}},
		"negative duration":  {Dur: -1},
	}
	for name, in := range cases {
		r := newRig(t)
		in.FileName, in.ContentType, in.Size = "clip.mp4", "video/mp4", 40*mib
		if _, err := r.svc.Start(t.Context(), an, in); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: err = %v", name, err)
		}
		if r.objects.callCount() != 0 {
			t.Errorf("%s: storage called %d times", name, r.objects.callCount())
		}
	}
}

func TestCompleteRefusesABadHashBeforeStorage(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	before := r.objects.callCount()
	_, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3), "sha256:"+strings.ToUpper(firstHash[7:]))
	if !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
	if calls := r.objects.callCount() - before; calls != 0 {
		t.Fatalf("storage called %d times", calls)
	}
	if got, _ := r.uploads.Get(t.Context(), s.Upload.Key); got.State != domain.StateUploading || got.Hash != "" {
		t.Fatalf("upload changed: %+v", got)
	}
}

func TestCompleteKeepsTheFirstHash(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	done, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3), firstHash)
	if err != nil || done.Hash != firstHash {
		t.Fatalf("complete = %+v, %v", done, err)
	}
	again, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3), secondHash)
	if err != nil || again.Hash != firstHash {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	if got, _ := r.uploads.Get(t.Context(), s.Upload.Key); got.Hash != firstHash || got.Ver != 2 {
		t.Fatalf("stored %+v", got)
	}
}

func TestCompleteWithoutAHashStoresNone(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	if _, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.uploads.Get(t.Context(), s.Upload.Key); got.State != domain.StateUploaded || got.Hash != "" {
		t.Fatalf("stored %+v", got)
	}
}
