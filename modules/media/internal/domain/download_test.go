package domain_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestFileDispositionShowsOnlySafeMediaInline(t *testing.T) {
	cases := []struct {
		name, mime string
		want       domain.Disposition
	}{
		{"a.png", "image/png", domain.Disposition{ContentType: "image/png", Disposition: "inline"}},
		{"a.jpg", "image/jpeg", domain.Disposition{ContentType: "image/jpeg", Disposition: "inline"}},
		{"a.gif", "image/gif", domain.Disposition{ContentType: "image/gif", Disposition: "inline"}},
		{"a.webp", "image/webp", domain.Disposition{ContentType: "image/webp", Disposition: "inline"}},
		{"a.mp4", "video/mp4", domain.Disposition{ContentType: "video/mp4", Disposition: "inline"}},
		{"logo.svg", "image/svg+xml", domain.Disposition{ContentType: "application/octet-stream", Disposition: `attachment; filename="logo.svg"; filename*=UTF-8''logo.svg`}},
		{"page.html", "text/html", domain.Disposition{ContentType: "application/octet-stream", Disposition: `attachment; filename="page.html"; filename*=UTF-8''page.html`}},
		{"report.pdf", "application/pdf", domain.Disposition{ContentType: "application/octet-stream", Disposition: `attachment; filename="report.pdf"; filename*=UTF-8''report.pdf`}},
		{"x.png", "image/png; charset=x", domain.Disposition{ContentType: "application/octet-stream", Disposition: `attachment; filename="x.png"; filename*=UTF-8''x.png`}},
		{"x.mov", "video/quicktime", domain.Disposition{ContentType: "application/octet-stream", Disposition: `attachment; filename="x.mov"; filename*=UTF-8''x.mov`}},
	}
	for _, c := range cases {
		if got := domain.FileDisposition(c.name, c.mime); got != c.want {
			t.Errorf("FileDisposition(%q, %q) = %+v, want %+v", c.name, c.mime, got, c.want)
		}
	}
}

func TestFileDispositionEncodesAccentsAndQuotes(t *testing.T) {
	got := domain.FileDisposition(`Báo cáo "Q4" 100%;v2\x.pdf`, "application/pdf")
	want := `attachment; filename="B_o c_o _Q4_ 100_;v2_x.pdf"; filename*=UTF-8''B%C3%A1o%20c%C3%A1o%20%22Q4%22%20100%25%3Bv2%5Cx.pdf`
	if got.ContentType != "application/octet-stream" || got.Disposition != want {
		t.Fatalf("got %+v\nwant %s", got, want)
	}
}

func TestThumbnailDispositionIsInlineWithoutAType(t *testing.T) {
	if got := domain.ThumbnailDisposition(); got != (domain.Disposition{Disposition: "inline"}) {
		t.Fatalf("got %+v", got)
	}
}

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
