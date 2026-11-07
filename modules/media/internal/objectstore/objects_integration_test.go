package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

const partSize = 5 << 20

func itConfig(t *testing.T) Config {
	t.Helper()
	endpoint := os.Getenv("CHATIM_IT_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set CHATIM_IT_S3_ENDPOINT to run")
	}
	c := Config{
		Endpoint: endpoint, PublicEndpoint: endpoint, Region: "us-east-1",
		AccessKey: os.Getenv("CHATIM_IT_S3_ACCESS_KEY"), SecretKey: os.Getenv("CHATIM_IT_S3_SECRET_KEY"),
		Bucket: itBucket(t, "p"), CompletedBucket: itBucket(t, "c"),
	}
	admin, _, err := client(endpoint, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{c.Bucket, c.CompletedBucket} {
		if err := admin.MakeBucket(t.Context(), b, minio.MakeBucketOptions{Region: c.Region}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dropBucket(admin, b) })
	}
	return c
}

func itBucket(t *testing.T, kind string) string {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "chatim-it-" + kind + "-" + hex.EncodeToString(b)
}

func dropBucket(c *minio.Client, bucket string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for o := range c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		_ = c.RemoveObject(ctx, bucket, o.Key, minio.RemoveObjectOptions{})
	}
	for u := range c.ListIncompleteUploads(ctx, bucket, "", true) {
		_ = c.RemoveIncompleteUpload(ctx, bucket, u.Key)
	}
	_ = c.RemoveBucket(ctx, bucket)
}

func putPart(t *testing.T, url string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Expect", "100-continue")
	resp, err := (&http.Client{Transport: &http.Transport{ExpectContinueTimeout: 5 * time.Second}}).Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("ETag")
}

func TestMultipartLifecycleOnS3(t *testing.T) {
	c := itConfig(t)
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, key := t.Context(), "acme/an/20261106/itlifecycle0000000000000000"
	if err := s.CheckBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := s.StartMultipart(ctx, key, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int64{partSize, partSize, 1 << 20}
	body := make([]byte, partSize+1)
	_, _ = rand.Read(body)
	first, err := s.SignPart(ctx, key, id, 1, sizes[0], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := putPart(t, first, body); status != http.StatusForbidden {
		t.Fatalf("PUT one byte over the signed size: status %d", status)
	}
	var parts []domain.UploadedPart
	for i, size := range sizes {
		url, err := s.SignPart(ctx, key, id, int32(i+1), size, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		status, etag := putPart(t, url, body[:size])
		if status != http.StatusOK || etag == "" {
			t.Fatalf("part %d status %d", i+1, status)
		}
		parts = append(parts, domain.UploadedPart{Number: int32(i + 1), ETag: etag})
	}
	wrong := append([]domain.UploadedPart(nil), parts...)
	wrong[1].ETag = `"00000000000000000000000000000000"`
	if err := s.Complete(ctx, key, id, wrong); !errors.Is(err, ErrInvalidPart) {
		t.Fatalf("wrong ETag: %v", err)
	}
	if err := s.Complete(ctx, key, id, parts); err != nil {
		t.Fatal(err)
	}
	if size, err := s.Head(ctx, key); err != nil || size != 11<<20 {
		t.Fatalf("head = %d, %v", size, err)
	}
	if err := s.Complete(ctx, key, id, parts); !errors.Is(err, ErrNoSuchUpload) {
		t.Fatalf("second complete: %v", err)
	}
	if err := s.Abort(ctx, key, id); !errors.Is(err, ErrNoSuchUpload) {
		t.Fatalf("abort after complete: %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head after delete: %v", err)
	}
}

func TestCheckBucketsTellsWhyStorageIsNotReady(t *testing.T) {
	c := itConfig(t)
	missing := c
	missing.CompletedBucket = "chatim-it-missing-bucket"
	badKey := c
	badKey.SecretKey = "wrong-" + c.SecretKey
	down := c
	down.Endpoint = "http://127.0.0.1:1"
	cases := map[string]struct {
		cfg  Config
		want error
	}{
		"missing bucket": {missing, ErrBucketMissing},
		"wrong secret":   {badKey, ErrAccess},
		"server down":    {down, ErrUnavailable},
	}
	for name, tc := range cases {
		s, err := New(tc.cfg)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err = s.CheckBuckets(ctx)
		cancel()
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
