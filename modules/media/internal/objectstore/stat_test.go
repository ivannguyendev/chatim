package objectstore

import (
	"net/http"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestStatReadsTheSizeAndTypeFromTheChosenBucket(t *testing.T) {
	f := &fakeS3{headSize: 4096, headType: "image/webp"}
	s := fakeStore(t, f)
	thumb := copyKey + ".thumb"
	obj, found, err := s.Stat(t.Context(), domain.BucketProcessing, thumb)
	if err != nil || !found || obj != (domain.Object{Size: 4096, ContentType: "image/webp"}) {
		t.Fatalf("stat = %+v %v %v", obj, found, err)
	}
	if ok, err := s.Exists(t.Context(), domain.BucketCompleted, thumb); !ok || err != nil {
		t.Fatalf("exists = %v %v", ok, err)
	}
	want := []s3Call{{Method: http.MethodHead, Path: "/processing/" + thumb}, {Method: http.MethodHead, Path: "/completed/" + thumb}}
	if got := f.recorded(); !slices.Equal(got, want) {
		t.Fatalf("calls %+v", got)
	}
}

func TestStatOfAMissingObjectIsNotFoundWithoutAnError(t *testing.T) {
	s := fakeStore(t, &fakeS3{headMissing: true})
	obj, found, err := s.Stat(t.Context(), domain.BucketProcessing, copyKey)
	if err != nil || found || obj != (domain.Object{}) {
		t.Fatalf("stat = %+v %v %v", obj, found, err)
	}
	if c := counted(s, "head"); c["client"] != 1 {
		t.Fatalf("head stats %v", c)
	}
	if _, _, err := s.Stat(t.Context(), domain.Bucket("other"), copyKey); err == nil {
		t.Fatal("unknown bucket accepted")
	}
}
