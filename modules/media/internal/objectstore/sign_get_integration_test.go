package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
)

type itChat struct{ files []domain.File }

func (c itChat) VisibleAttachments(_ context.Context, _, _ string, refs []domain.MessageRef) ([]domain.MessageFiles, error) {
	return []domain.MessageFiles{{Ref: refs[0], Files: c.files}}, nil
}

type fetched struct {
	status      int
	body        []byte
	contentType string
}

func fetch(t *testing.T, url string) fetched {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return fetched{resp.StatusCode, body, resp.Header.Get("Content-Type")}
}

func putObject(t *testing.T, s *S3, key, contentType string) []byte {
	t.Helper()
	body := make([]byte, 64<<10)
	_, _ = rand.Read(body)
	if _, err := s.api.PutObject(t.Context(), s.bucket, key, bytes.NewReader(body), int64(len(body)), "", "", minio.PutObjectOptions{ContentType: contentType}); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return body
}

func TestSignedDownloadsServeTheStoredBytesAndType(t *testing.T) {
	s, err := New(itConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	png, html, pdf, jpg := "acme/an/20261107/itpng", "acme/an/20261107/ithtml", "acme/an/20261107/itpdf", "acme/an/20261107/itjpg"
	pngBody := putObject(t, s, png, "image/png")
	htmlBody := putObject(t, s, html, "text/html")
	putObject(t, s, pdf, "application/pdf")
	putObject(t, s, jpg, "image/jpeg")
	thumbBody := putObject(t, s, png+".thumb", "image/webp")
	files := []domain.File{
		{Key: png, Name: "a.png", Mime: "image/png"},
		{Key: html, Name: "trang.html", Mime: "text/html"},
		{Key: pdf, Name: "r.pdf", Mime: "application/pdf"},
		{Key: jpg, Name: "b.jpg", Mime: "image/jpeg"},
	}
	svc, err := download.New(download.Config{SignatureTTL: time.Minute, Thumbnails: domain.Thumbnails{Enabled: true, Suffix: ".thumb"}}, download.Deps{Chat: itChat{files: files}, Objects: s})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.AttachmentURLs(t.Context(), domain.Caller{Tenant: "acme", User: "binh"}, []domain.MessageRef{{Room: "7", Seq: 1}})
	if err != nil {
		t.Fatal(err)
	}
	urls := got.Messages[0].Files
	image := fetch(t, urls[0].URL)
	if image.status != http.StatusOK || !bytes.Equal(image.body, pngBody) || image.contentType != "image/png" {
		t.Fatalf("png: %d %s", image.status, image.contentType)
	}
	page := fetch(t, urls[1].URL)
	if page.status != http.StatusOK || !bytes.Equal(page.body, htmlBody) || page.contentType != "text/html" {
		t.Fatalf("html: %d %s", page.status, page.contentType)
	}
	thumb := fetch(t, urls[0].ThumbnailURL)
	if thumb.status != http.StatusOK || !bytes.Equal(thumb.body, thumbBody) || thumb.contentType != "image/webp" {
		t.Fatalf("thumbnail: %d %s", thumb.status, thumb.contentType)
	}
	if urls[1].ThumbnailURL != "" || urls[2].ThumbnailURL != "" {
		t.Fatalf("html or pdf thumbnail: %+v", urls)
	}
	if notMade := fetch(t, urls[3].ThumbnailURL); notMade.status != http.StatusNotFound {
		t.Fatalf("thumbnail never written: status %d", notMade.status)
	}
}
