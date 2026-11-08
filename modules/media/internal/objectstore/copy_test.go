package objectstore

import (
	"errors"
	"net/http"
	"slices"
	"testing"
)

const copyKey = "acme/an/20261108/0123456789abcdefghijklmnop"

func TestCopyUpToTheSingleMaxIsOneCopyWithTheUploadType(t *testing.T) {
	f := &fakeS3{headSize: 3 << 20}
	s := fakeStore(t, f)
	if err := s.Copy(t.Context(), copyKey, 3<<20, "image/png"); err != nil {
		t.Fatal(err)
	}
	want := []s3Call{
		{Method: http.MethodPut, Path: "/completed/" + copyKey, Source: "/processing/" + copyKey, ContentType: "image/png", Directive: "REPLACE"},
		{Method: http.MethodHead, Path: "/completed/" + copyKey},
	}
	got := f.recorded()
	for i := range got {
		got[i].Source = trimSlash(got[i].Source)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("calls %+v", got)
	}
	if c := counted(s, "copy"); c["ok"] != 1 {
		t.Fatalf("copy stats %v", c)
	}
}

func TestCopyAboveTheSingleMaxCopiesByteRangesIntoOneMultipartUpload(t *testing.T) {
	f := &fakeS3{headSize: 12 << 20}
	s := fakeStore(t, f)
	if err := s.Copy(t.Context(), copyKey, 12<<20, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	got := f.recorded()
	if len(got) != 6 || got[0].Method != http.MethodPost || got[0].ContentType != "video/mp4" || got[4].Method != http.MethodPost || got[5].Method != http.MethodHead {
		t.Fatalf("calls %+v", got)
	}
	ranges := []string{"bytes=0-5242879", "bytes=5242880-10485759", "bytes=10485760-12582911"}
	for i, r := range ranges {
		part := got[i+1]
		if part.Method != http.MethodPut || part.Range != r || part.PartNumber != string(rune('1'+i)) || trimSlash(part.Source) != "/processing/"+copyKey || part.Path != "/completed/"+copyKey {
			t.Fatalf("part %d: %+v", i+1, part)
		}
	}
}

func TestCopyAbortsTheMultipartUploadWhenAPartFails(t *testing.T) {
	f := &fakeS3{failPart: "2", headSize: 12 << 20}
	s := fakeStore(t, f)
	err := s.Copy(t.Context(), copyKey, 12<<20, "video/mp4")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	got := f.recorded()
	if len(got) != 4 || got[3].Method != http.MethodDelete || got[3].Path != "/completed/"+copyKey {
		t.Fatalf("calls %+v", got)
	}
	if c := counted(s, "copy"); c["error"] != 1 {
		t.Fatalf("copy stats %v", c)
	}
	if c := counted(s, "abort"); c["ok"] != 1 {
		t.Fatalf("abort stats %v", c)
	}
}

func TestCopyFailsWhenTheDestinationSizeDiffers(t *testing.T) {
	s := fakeStore(t, &fakeS3{headSize: 3<<20 - 1})
	if err := s.Copy(t.Context(), copyKey, 3<<20, "image/png"); !errors.Is(err, ErrCopySize) {
		t.Fatalf("err = %v", err)
	}
}

func trimSlash(source string) string {
	if source == "" || source[0] == '/' {
		return source
	}
	return "/" + source
}
