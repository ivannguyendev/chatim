package objectstore

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type rangeRecorder struct {
	base   http.RoundTripper
	mu     sync.Mutex
	ranges []string
}

func (r *rangeRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if v := req.Header.Get("X-Amz-Copy-Source-Range"); v != "" {
		r.mu.Lock()
		r.ranges = append(r.ranges, v)
		r.mu.Unlock()
	}
	return r.base.RoundTrip(req)
}

func (r *rangeRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.ranges
	r.ranges = nil
	return out
}

func recordedStore(t *testing.T) (*S3, *rangeRecorder) {
	t.Helper()
	c := itConfig(t)
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	_, transport, err := client(c.Endpoint, c)
	if err != nil {
		t.Fatal(err)
	}
	rec := &rangeRecorder{base: transport}
	api, err := minio.New(s.api.EndpointURL().Host, &minio.Options{
		Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: s.api.EndpointURL().Scheme == "https",
		Region: c.Region, BucketLookup: minio.BucketLookupPath, MaxRetries: 1, Transport: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.api = minio.Core{Client: api}
	return s, rec
}

func putSource(t *testing.T, s *S3, key string, size int, contentType string) []byte {
	t.Helper()
	body := make([]byte, size)
	_, _ = rand.Read(body)
	if _, err := s.api.PutObject(t.Context(), s.bucket, key, bytes.NewReader(body), int64(size), "", "", minio.PutObjectOptions{ContentType: contentType}); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return body
}

func checkCompleted(t *testing.T, s *S3, key string, body []byte, contentType string) {
	t.Helper()
	r, info, _, err := s.api.GetObject(t.Context(), s.completed, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) || info.Size != int64(len(body)) || info.ContentType != contentType {
		t.Fatalf("%s: %d bytes, same %v, type %q", key, info.Size, bytes.Equal(got, body), info.ContentType)
	}
}

func TestCopyMovesBytesAndTypeToCompletedOnS3(t *testing.T) {
	s, rec := recordedStore(t)
	ctx := t.Context()
	cases := []struct {
		key, contentType string
		size             int
		ranges           []string
	}{
		{"acme/an/20261108/itcopysmall", "image/png", 3 << 20, nil},
		{"acme/an/20261108/itcopylarge", "video/mp4", 12 << 20, []string{"bytes=0-5242879", "bytes=5242880-10485759", "bytes=10485760-12582911"}},
	}
	for _, tc := range cases {
		body := putSource(t, s, tc.key, tc.size, tc.contentType)
		for round := range 2 {
			if err := s.Copy(ctx, tc.key, int64(tc.size), tc.contentType); err != nil {
				t.Fatalf("%s copy %d: %v", tc.key, round+1, err)
			}
			if got := rec.take(); !slices.Equal(got, tc.ranges) {
				t.Fatalf("%s copy %d ranges %v", tc.key, round+1, got)
			}
			checkCompleted(t, s, tc.key, body, tc.contentType)
		}
		t.Logf("%s: %d bytes copied with ranges %v", tc.key, tc.size, tc.ranges)
		if err := s.DeleteProcessing(ctx, tc.key); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Head(ctx, tc.key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("head after delete: %v", err)
		}
		inProcessing, errP := s.Exists(ctx, domain.BucketProcessing, tc.key)
		inCompleted, errC := s.Exists(ctx, domain.BucketCompleted, tc.key)
		if inProcessing || !inCompleted || errP != nil || errC != nil {
			t.Fatalf("exists processing %v %v, completed %v %v", inProcessing, errP, inCompleted, errC)
		}
	}
}

func TestCopyFailingMidwayLeavesNoMultipartUploadOnS3(t *testing.T) {
	s, rec := recordedStore(t)
	ctx, key := t.Context(), "acme/an/20261108/itcopybroken"
	putSource(t, s, key, 7<<20, "video/mp4")
	if err := s.Copy(ctx, key, 12<<20, "video/mp4"); err == nil {
		t.Fatal("copy past the end of the source succeeded")
	} else {
		t.Logf("copy error: %v; ranges sent %v", err, rec.take())
	}
	if c := counted(s, "abort"); c["ok"] != 1 {
		t.Fatalf("abort stats %v", c)
	}
	open, err := s.api.ListMultipartUploads(ctx, s.completed, key, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Uploads) != 0 {
		t.Fatalf("multipart uploads left: %+v", open.Uploads)
	}
	if ok, err := s.Exists(ctx, domain.BucketCompleted, key); ok || err != nil {
		t.Fatalf("completed object after a failed copy: %v %v", ok, err)
	}
}

func TestCopyOfAMissingSourceIsNotFoundOnS3(t *testing.T) {
	s, _ := recordedStore(t)
	if err := s.Copy(t.Context(), "acme/an/20261108/itcopymissing", 1<<20, "image/png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
