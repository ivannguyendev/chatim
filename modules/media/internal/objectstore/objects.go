package objectstore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type Config struct {
	Endpoint        string
	PublicEndpoint  string
	Region          string
	AccessKey       string
	SecretKey       string
	Bucket          string
	CompletedBucket string
}

type S3 struct {
	api       minio.Core
	signer    *minio.Client
	bucket    string
	completed string
	stats     Stats
}

func New(c Config) (*S3, error) {
	api, err := client(c.Endpoint, c)
	if err != nil {
		return nil, fmt.Errorf("objectstore endpoint: %w", err)
	}
	signer, err := client(c.PublicEndpoint, c)
	if err != nil {
		return nil, fmt.Errorf("objectstore public endpoint: %w", err)
	}
	return &S3{api: minio.Core{Client: api}, signer: signer, bucket: c.Bucket, completed: c.CompletedBucket}, nil
}

func client(endpoint string, c Config) (*minio.Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("endpoint %q needs http or https and a host", endpoint)
	}
	return minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure:       u.Scheme == "https",
		Region:       c.Region,
		BucketLookup: minio.BucketLookupPath,
		MaxRetries:   1,
	})
}

func (s *S3) Stats() *Stats { return &s.stats }

func (s *S3) StartMultipart(ctx context.Context, key, contentType string) (string, error) {
	id, err := s.api.NewMultipartUpload(ctx, s.bucket, key, minio.PutObjectOptions{ContentType: contentType})
	err = classify(err)
	s.stats.record(opStart, err)
	return id, err
}

func (s *S3) SignPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (string, error) {
	params := url.Values{"partNumber": {strconv.Itoa(int(number))}, "uploadId": {uploadID}}
	header := http.Header{"Content-Length": {strconv.FormatInt(size, 10)}}
	u, err := s.signer.PresignHeader(ctx, http.MethodPut, s.bucket, key, ttl, params, header)
	if err != nil {
		return "", fmt.Errorf("sign part %d: %w", number, err)
	}
	return u.String(), nil
}

func (s *S3) Complete(ctx context.Context, key, uploadID string, parts []domain.UploadedPart) error {
	done := make([]minio.CompletePart, len(parts))
	for i, p := range parts {
		done[i] = minio.CompletePart{PartNumber: int(p.Number), ETag: p.ETag}
	}
	_, err := s.api.CompleteMultipartUpload(ctx, s.bucket, key, uploadID, done, minio.PutObjectOptions{})
	err = classify(err)
	s.stats.record(opComplete, err)
	return err
}

func (s *S3) Abort(ctx context.Context, key, uploadID string) error {
	err := classify(s.api.AbortMultipartUpload(ctx, s.bucket, key, uploadID))
	s.stats.record(opAbort, err)
	return err
}

func (s *S3) Head(ctx context.Context, key string) (int64, error) {
	info, err := s.api.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	err = classify(err)
	s.stats.record(opHead, err)
	return info.Size, err
}

func (s *S3) Delete(ctx context.Context, key string) error {
	err := classify(s.api.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}))
	s.stats.record(opDelete, err)
	return err
}

func (s *S3) CheckBuckets(ctx context.Context) error {
	err := errors.Join(s.checkBucket(ctx, s.bucket), s.checkBucket(ctx, s.completed))
	s.stats.record(opCheckBuckets, err)
	return err
}

func (s *S3) checkBucket(ctx context.Context, bucket string) error {
	ok, err := s.api.BucketExists(ctx, bucket)
	switch {
	case err != nil:
		return fmt.Errorf("bucket %s: %w", bucket, classify(err))
	case !ok:
		return fmt.Errorf("bucket %s: %w", bucket, ErrBucketMissing)
	}
	return nil
}
