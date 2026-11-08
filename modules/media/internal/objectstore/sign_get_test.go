package objectstore

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

var testCopyLimits = domain.CopyLimits{SingleMax: 5 << 20, PartBytes: 5 << 20}

func signerOnly(t *testing.T) *S3 {
	t.Helper()
	s, err := New(Config{
		Endpoint: "http://chatim-s3:3900", PublicEndpoint: "https://media.example.com", Region: "us-east-1",
		AccessKey: "GK1", SecretKey: "secret", Bucket: "processing", CompletedBucket: "completed", Copy: testCopyLimits,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func signedQuery(t *testing.T, bucket domain.Bucket, key string, ttl time.Duration) (*url.URL, url.Values) {
	t.Helper()
	signed, err := signerOnly(t).SignGet(t.Context(), bucket, key, ttl)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	return u, u.Query()
}

func TestSignGetSignsAPlainURLOnThePublicEndpoint(t *testing.T) {
	u, q := signedQuery(t, domain.BucketProcessing, "acme/an/20261107/0123456789abcdefghijklmnop", time.Hour)
	if u.Host != "media.example.com" || u.Path != "/processing/acme/an/20261107/0123456789abcdefghijklmnop" {
		t.Fatalf("url %s", u)
	}
	if q.Has("response-content-type") || q.Has("response-content-disposition") || q.Get("X-Amz-Expires") != "3600" {
		t.Fatalf("query %v", q)
	}
	if !strings.Contains(q.Get("X-Amz-SignedHeaders"), "host") || q.Get("X-Amz-Signature") == "" {
		t.Fatalf("not signed: %v", q)
	}
}

func TestSignGetForAThumbnailIsPlainToo(t *testing.T) {
	_, q := signedQuery(t, domain.BucketProcessing, "acme/an/20261107/0123456789abcdefghijklmnop.thumb", time.Minute)
	if q.Has("response-content-type") || q.Has("response-content-disposition") || q.Get("X-Amz-Expires") != "60" {
		t.Fatalf("query %v", q)
	}
}

func TestSignGetPicksTheBucketTheFileLivesIn(t *testing.T) {
	key := "acme/an/20261107/0123456789abcdefghijklmnop"
	for bucket, path := range map[domain.Bucket]string{"": "/processing/" + key, domain.BucketProcessing: "/processing/" + key, domain.BucketCompleted: "/completed/" + key} {
		if u, _ := signedQuery(t, bucket, key, time.Minute); u.Path != path {
			t.Errorf("bucket %q: path %s", bucket, u.Path)
		}
	}
	if _, err := signerOnly(t).SignGet(t.Context(), "archive", key, time.Minute); !errors.Is(err, ErrUnknownBucket) {
		t.Fatalf("unknown bucket: %v", err)
	}
}

func TestNewRefusesCopyLimitsS3Rejects(t *testing.T) {
	c := Config{Endpoint: "http://chatim-s3:3900", PublicEndpoint: "http://chatim-s3:3900", Region: "us-east-1", Bucket: "p", CompletedBucket: "c"}
	if _, err := New(c); err == nil {
		t.Fatal("zero copy limits accepted")
	}
}
