package main

import (
	"context"
	"net/http"
	"time"
)

type part struct {
	number int32
	etag   string
}

type signedPart struct {
	url    string
	header http.Header
}

type objectStore interface {
	start(ctx context.Context, key, contentType string) (string, error)
	signPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (signedPart, error)
	complete(ctx context.Context, key, uploadID string, parts []part) error
	abort(ctx context.Context, key, uploadID string) error
	head(ctx context.Context, bucket, key string) (int64, error)
	headBucket(ctx context.Context, bucket string) error
	copyObject(ctx context.Context, srcBucket, dstBucket, key string) error
	putCORS(ctx context.Context, bucket, origin string) error
	putLifecycle(ctx context.Context, bucket string) error
	code(err error) string
}

type storeConfig struct {
	endpoint, region  string
	access, secret    string
	bucket, completed string
}
