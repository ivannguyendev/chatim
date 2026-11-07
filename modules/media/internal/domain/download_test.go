package domain_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestThumbnailsOnlyForImagesAndVideosWhenOn(t *testing.T) {
	key := "acme/an/20261107/0123456789abcdefghijklmnop"
	on := domain.Thumbnails{Enabled: true, Suffix: ".small"}
	for mime, want := range map[string]string{"image/png": key + ".small", "video/mp4": key + ".small", "image/svg+xml": key + ".small", "application/pdf": "", "text/html": ""} {
		if got, ok := on.KeyFor(key, mime); got != want || ok != (want != "") {
			t.Errorf("on %s = %q, %v; want %q", mime, got, ok, want)
		}
	}
	off := domain.Thumbnails{Suffix: ".thumb"}
	if got, ok := off.KeyFor(key, "image/png"); got != "" || ok {
		t.Fatalf("off = %q, %v", got, ok)
	}
}
