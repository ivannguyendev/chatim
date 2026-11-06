package upload_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/objectstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func TestStartPlansPartsAndSignsTheFirstRound(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	if !strings.HasPrefix(s.Upload.Key, "acme/an/20261106/") || len(s.Parts) != 3 || len(s.URLs) != 3 {
		t.Fatalf("started %+v", s)
	}
	if !s.ExpiresAt.Equal(now.Add(15*60e9)) || s.Upload.PartSize != 16*mib {
		t.Fatalf("expires %v part size %d", s.ExpiresAt, s.Upload.PartSize)
	}
	want := []signed{{1, 16 * mib}, {2, 16 * mib}, {3, 8 * mib}}
	for i, w := range want {
		if r.objects.signed[i] != w {
			t.Fatalf("signed %v", r.objects.signed)
		}
	}
	if r.svc.Started() != 1 || r.svc.Completed() != 0 {
		t.Fatalf("started %d completed %d", r.svc.Started(), r.svc.Completed())
	}
	stored, err := r.uploads.Get(t.Context(), s.Upload.Key)
	if err != nil || stored.State != domain.StateUploading || stored.Ver != 1 || stored.UploadID == "" {
		t.Fatalf("stored %+v, %v", stored, err)
	}
}

func TestStartSignsAtMostOneRoundOfStreams(t *testing.T) {
	r := newRig(t)
	s, err := r.svc.Start(t.Context(), an, upload.StartInput{FileName: "big.mp4", ContentType: "video/mp4", Size: 2 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Parts) != 64 || len(s.URLs) != 4 || s.URLs[3].Number != 4 {
		t.Fatalf("parts %d urls %v", len(s.Parts), s.URLs)
	}
}

func TestStartRefusals(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.Start(t.Context(), an, upload.StartInput{FileName: "", ContentType: "video/mp4", Size: 1}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := r.svc.Start(t.Context(), an, upload.StartInput{FileName: "a", ContentType: "video/mp4", Size: 5 << 30}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("too big: %v", err)
	}
	r.ready = false
	if _, err := r.svc.Start(t.Context(), an, upload.StartInput{FileName: "a", ContentType: "video/mp4", Size: 1}); !errors.Is(err, domain.ErrNotReady) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("not ready: %v", err)
	}
	r.ready = true
	r.objects.set(func(f *fakeObjects) { f.startErr = objectstore.ErrUnavailable })
	_, err := r.svc.Start(t.Context(), an, upload.StartInput{FileName: "a", ContentType: "video/mp4", Size: 1})
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("storage down: %v", err)
	}
}

func TestStartAbortsTheSessionWhenTheDocCannotBeWritten(t *testing.T) {
	r := newRig(t)
	first := r.start(t)
	clash, err := upload.New(upload.Config{Limits: domain.Limits{MinPartBytes: 16 * mib, MaxPartBytes: 128 * mib, MaxParts: 2000, MaxParallel: 4, MaxFileBytes: 1 << 30}, PartURLTTL: 60e9},
		upload.Deps{Uploads: r.uploads, Objects: r.objects, Now: func() time.Time { return now }, Random: func() [16]byte { return [16]byte{1} }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = clash.Start(t.Context(), an, upload.StartInput{FileName: "b", ContentType: "video/mp4", Size: 1})
	if !errors.Is(err, apperr.ErrUnavailable) || r.objects.aborts != 1 {
		t.Fatalf("err %v aborts %d", err, r.objects.aborts)
	}
	if got, _ := r.uploads.Get(t.Context(), first.Upload.Key); got.FileName != "clip.mp4" {
		t.Fatalf("first upload changed: %+v", got)
	}
}

func TestPartURLs(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	urls, expires, err := r.svc.PartURLs(t.Context(), an, s.Upload.Key, []int32{3})
	if err != nil || len(urls) != 1 || urls[0].Number != 3 || expires.IsZero() {
		t.Fatalf("urls %v %v %v", urls, expires, err)
	}
	if last := r.objects.signed[len(r.objects.signed)-1]; last != (signed{3, 8 * mib}) {
		t.Fatalf("signed %v", last)
	}
	cases := map[string]struct {
		who  upload.Caller
		key  string
		nums []int32
		want error
	}{
		"someone else's key": {binh, s.Upload.Key, []int32{1}, apperr.ErrNotFound},
		"forged key":         {an, "acme/an/20261106/zzzzzzzzzzzzzzzzzzzzzzzzzz", []int32{1}, apperr.ErrNotFound},
		"more than N":        {an, s.Upload.Key, []int32{1, 2, 3, 3, 3}, apperr.ErrInvalidArgument},
		"past the last part": {an, s.Upload.Key, []int32{4}, apperr.ErrInvalidArgument},
	}
	for name, c := range cases {
		if _, _, err := r.svc.PartURLs(t.Context(), c.who, c.key, c.nums); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.PartURLs(t.Context(), an, s.Upload.Key, []int32{1}); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("after complete: %v", err)
	}
}
