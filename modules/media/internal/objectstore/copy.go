package objectstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/minio/minio-go/v7"
)

func (s *S3) Copy(ctx context.Context, key string, size int64, contentType string) error {
	err := s.copyToCompleted(ctx, key, size, contentType)
	s.stats.record(opCopy, err)
	return err
}

func (s *S3) copyToCompleted(ctx context.Context, key string, size int64, contentType string) error {
	var err error
	if s.copy.Whole(size) {
		err = s.copyWhole(ctx, key, contentType)
	} else {
		err = s.copyParts(ctx, key, size, contentType)
	}
	if err != nil {
		return err
	}
	info, err := s.api.StatObject(ctx, s.completed, key, minio.StatObjectOptions{})
	if err != nil {
		return classify(err)
	}
	if info.Size != size {
		return fmt.Errorf("%w: %s has %d bytes, want %d", ErrCopySize, key, info.Size, size)
	}
	return nil
}

func (s *S3) copyWhole(ctx context.Context, key, contentType string) error {
	header := map[string]string{"Content-Type": contentType, "X-Amz-Metadata-Directive": "REPLACE"}
	_, err := s.api.CopyObject(ctx, s.bucket, key, s.completed, key, header, minio.CopySrcOptions{}, minio.PutObjectOptions{})
	return classify(err)
}

func (s *S3) copyParts(ctx context.Context, key string, size int64, contentType string) error {
	ranges, err := s.copy.Parts(size)
	if err != nil {
		return fmt.Errorf("objectstore: %w", err)
	}
	id, err := s.api.NewMultipartUpload(ctx, s.completed, key, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return classify(err)
	}
	parts := make([]minio.CompletePart, 0, len(ranges))
	for i, r := range ranges {
		part, err := s.api.CopyObjectPart(ctx, s.bucket, key, s.completed, key, id, i+1, r.Start, r.Length, nil)
		if err != nil {
			return s.abortCopy(ctx, key, id, err)
		}
		parts = append(parts, part)
	}
	if _, err := s.api.CompleteMultipartUpload(ctx, s.completed, key, id, parts, minio.PutObjectOptions{}); err != nil {
		return s.abortCopy(ctx, key, id, err)
	}
	return nil
}

func (s *S3) abortCopy(ctx context.Context, key, uploadID string, cause error) error {
	err := classify(s.api.AbortMultipartUpload(context.WithoutCancel(ctx), s.completed, key, uploadID))
	s.stats.record(opAbort, err)
	if err != nil {
		return errors.Join(classify(cause), fmt.Errorf("abort copy: %w", err))
	}
	return classify(cause)
}
