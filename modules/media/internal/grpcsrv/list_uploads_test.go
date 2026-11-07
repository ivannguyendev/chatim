package grpcsrv_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestListUploadsOverTheService(t *testing.T) {
	s, uploads := newService(t)
	image := storetest.Uploaded("acme", "an", "0003", "image/png")
	image.FileName, image.Dim, image.Dur, image.Hash, image.CreatedAt = "a.png", []int32{640, 480}, 0, "sha256:"+strings.Repeat("ab", 32), time.Now().UTC()
	clip := storetest.Uploaded("acme", "an", "0002", "video/mp4")
	clip.CreatedAt = time.Now().UTC()
	old := storetest.Uploaded("acme", "an", "0001", "application/pdf")
	old.CreatedAt = time.Now().UTC().Add(-31 * 24 * time.Hour)
	for _, u := range []domain.Upload{image, clip, old, storetest.Uploaded("acme", "an2", "0004", "image/png")} {
		if err := uploads.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListUploads(caller("acme", "an"), &chatimv1.ListUploadsRequest{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	files := got.GetFiles()
	if len(files) != 2 || got.GetNextBefore() != clip.Key || !got.GetExpiresAt().IsValid() {
		t.Fatalf("got %v", got)
	}
	f := files[0]
	if f.GetKey() != image.Key || f.GetName() != "a.png" || f.GetMime() != "image/png" || f.GetSize() != image.Size ||
		!slices.Equal(f.GetDim(), image.Dim) || f.GetHash() != image.Hash || !f.GetCreatedAt().AsTime().Equal(image.CreatedAt) ||
		f.GetExpired() || f.GetUrl() != "https://s3/"+image.Key {
		t.Fatalf("file %v", f)
	}
	rest, err := s.ListUploads(caller("acme", "an"), &chatimv1.ListUploadsRequest{Before: got.GetNextBefore(), PageSize: 2})
	if err != nil || len(rest.GetFiles()) != 1 || !rest.GetFiles()[0].GetExpired() || rest.GetFiles()[0].GetUrl() != "" || rest.GetNextBefore() != "" {
		t.Fatalf("rest %v, %v", rest, err)
	}
}

func TestListUploadsMapsKinds(t *testing.T) {
	s, uploads := newService(t)
	for _, u := range []domain.Upload{
		storetest.Uploaded("acme", "an", "0003", "image/png"),
		storetest.Uploaded("acme", "an", "0002", "video/mp4"),
		storetest.Uploaded("acme", "an", "0001", "application/pdf"),
	} {
		if err := uploads.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[chatimv1.FileKind]string{
		chatimv1.FileKind_FILE_KIND_IMAGE: "image/png",
		chatimv1.FileKind_FILE_KIND_VIDEO: "video/mp4",
		chatimv1.FileKind_FILE_KIND_OTHER: "application/pdf",
	}
	for kind, mime := range cases {
		got, err := s.ListUploads(caller("acme", "an"), &chatimv1.ListUploadsRequest{Kinds: []chatimv1.FileKind{kind}, PageSize: 10})
		if err != nil || len(got.GetFiles()) != 1 || got.GetFiles()[0].GetMime() != mime {
			t.Fatalf("%v: %v, %v", kind, got, err)
		}
	}
	for _, bad := range []chatimv1.FileKind{chatimv1.FileKind_FILE_KIND_UNSPECIFIED, chatimv1.FileKind(9)} {
		req := &chatimv1.ListUploadsRequest{Kinds: []chatimv1.FileKind{chatimv1.FileKind_FILE_KIND_IMAGE, bad}, PageSize: 10}
		if _, err := s.ListUploads(caller("acme", "an"), req); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("kind %v: err = %v", bad, err)
		}
	}
	if _, err := s.ListUploads(caller("acme", "an"), &chatimv1.ListUploadsRequest{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("zero page size err = %v", err)
	}
}
