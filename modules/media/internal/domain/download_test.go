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

func TestMessageRefKeyJoinsRoomThreadAndSeq(t *testing.T) {
	if got := (domain.MessageRef{Room: "8812", Seq: 42}).Key(); got != "8812-0-42" {
		t.Fatalf("plain key = %q", got)
	}
	if got := (domain.MessageRef{Room: "8812", Thread: 7, Seq: 3}).Key(); got != "8812-7-3" {
		t.Fatalf("thread key = %q", got)
	}
}
