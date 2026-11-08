package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/cors"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

type minioStore struct {
	core   minio.Core
	bucket string
}

func newMinioStore(c storeConfig) (*minioStore, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(c.access, c.secret, ""),
		Secure:       u.Scheme == "https",
		Region:       c.region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, err
	}
	return &minioStore{core: minio.Core{Client: client}, bucket: c.bucket}, nil
}

func (s *minioStore) start(ctx context.Context, key, contentType string) (string, error) {
	return s.core.NewMultipartUpload(ctx, s.bucket, key, minio.PutObjectOptions{ContentType: contentType})
}

func (s *minioStore) signPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (signedPart, error) {
	params := url.Values{"partNumber": {strconv.Itoa(int(number))}, "uploadId": {uploadID}}
	header := http.Header{"Content-Length": {strconv.FormatInt(size, 10)}}
	u, err := s.core.PresignHeader(ctx, http.MethodPut, s.bucket, key, ttl, params, header)
	if err != nil {
		return signedPart{}, err
	}
	return signedPart{url: u.String(), header: http.Header{}}, nil
}

func (s *minioStore) complete(ctx context.Context, key, uploadID string, parts []part) error {
	done := make([]minio.CompletePart, len(parts))
	for i, p := range parts {
		done[i] = minio.CompletePart{PartNumber: int(p.number), ETag: p.etag}
	}
	_, err := s.core.CompleteMultipartUpload(ctx, s.bucket, key, uploadID, done, minio.PutObjectOptions{})
	return err
}

func (s *minioStore) abort(ctx context.Context, key, uploadID string) error {
	return s.core.AbortMultipartUpload(ctx, s.bucket, key, uploadID)
}

func (s *minioStore) head(ctx context.Context, bucket, key string) (int64, error) {
	info, err := s.core.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	return info.Size, err
}

func (s *minioStore) headBucket(ctx context.Context, bucket string) error {
	ok, err := s.core.BucketExists(ctx, bucket)
	if err == nil && !ok {
		return errMissingBucket
	}
	return err
}

func (s *minioStore) copyObject(ctx context.Context, srcBucket, dstBucket, key string) error {
	_, err := s.core.Client.CopyObject(ctx, minio.CopyDestOptions{Bucket: dstBucket, Object: key}, minio.CopySrcOptions{Bucket: srcBucket, Object: key})
	return err
}

func (s *minioStore) putCORS(ctx context.Context, bucket, origin string) error {
	rule := cors.Rule{AllowedOrigin: []string{origin}, AllowedMethod: []string{http.MethodPut, http.MethodGet}, AllowedHeader: []string{"*"}, ExposeHeader: []string{"ETag"}, MaxAgeSeconds: 3600}
	return s.core.SetBucketCors(ctx, bucket, cors.NewConfig([]cors.Rule{rule}))
}

func (s *minioStore) putLifecycle(ctx context.Context, bucket string) error {
	cfg := lifecycle.NewConfiguration()
	cfg.Rules = []lifecycle.Rule{{
		ID:                             "processing",
		Status:                         "Enabled",
		AbortIncompleteMultipartUpload: lifecycle.AbortIncompleteMultipartUpload{DaysAfterInitiation: 1},
		Expiration:                     lifecycle.Expiration{Days: 30},
		RuleFilter:                     lifecycle.Filter{Prefix: ""},
	}}
	return s.core.SetBucketLifecycle(ctx, bucket, cfg)
}

func (s *minioStore) code(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errMissingBucket) {
		return "NoSuchBucket"
	}
	if c := minio.ToErrorResponse(err).Code; c != "" {
		return c
	}
	if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
		return "network"
	}
	return "other:" + err.Error()
}
