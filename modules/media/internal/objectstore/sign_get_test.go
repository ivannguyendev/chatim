package objectstore

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func signerOnly(t *testing.T) *S3 {
	t.Helper()
	s, err := New(Config{
		Endpoint: "http://chatim-s3:3900", PublicEndpoint: "https://media.example.com", Region: "us-east-1",
		AccessKey: "GK1", SecretKey: "secret", Bucket: "processing", CompletedBucket: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignGetForcesTheResponseHeadersOnThePublicEndpoint(t *testing.T) {
	s := signerOnly(t)
	d := domain.FileDisposition("Báo cáo.pdf", "application/pdf")
	signed, err := s.SignGet(t.Context(), "acme/an/20261107/0123456789abcdefghijklmnop", time.Hour, d)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "media.example.com" || u.Path != "/processing/acme/an/20261107/0123456789abcdefghijklmnop" {
		t.Fatalf("url %s", signed)
	}
	if q.Get("response-content-type") != d.ContentType || q.Get("response-content-disposition") != d.Disposition || q.Get("X-Amz-Expires") != "3600" {
		t.Fatalf("query %v", q)
	}
	if !strings.Contains(q.Get("X-Amz-SignedHeaders"), "host") || q.Get("X-Amz-Signature") == "" {
		t.Fatalf("not signed: %v", q)
	}
}

func TestSignGetForAThumbnailLeavesTheTypeToStorage(t *testing.T) {
	signed, err := signerOnly(t).SignGet(t.Context(), "acme/an/20261107/0123456789abcdefghijklmnop.thumb", time.Minute, domain.ThumbnailDisposition())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if q := u.Query(); q.Has("response-content-type") || q.Get("response-content-disposition") != "inline" {
		t.Fatalf("query %v", q)
	}
}
