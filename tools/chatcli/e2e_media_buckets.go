package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	mediaMoveWait = 30 * time.Second
	mediaMovePoll = 500 * time.Millisecond
)

func bucketOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse signed URL: %w", err)
	}
	bucket, _, found := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	if !found || bucket == "" {
		return "", fmt.Errorf("signed URL path %q names no bucket", u.Path)
	}
	return bucket, nil
}

func inBucket(name, raw, want string) error {
	got, err := bucketOf(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if got != want {
		return fmt.Errorf("%s is in bucket %q, want %q", name, got, want)
	}
	return nil
}

func (r *mediaRun) sentFiles() []*mediaFile { return []*mediaFile{r.image, r.page} }

func (r *mediaRun) movedToCompleted() error {
	deadline := time.Now().Add(mediaMoveWait)
	for {
		files, err := r.messageFiles(r.b.gw, r.room, r.seq)
		if err != nil {
			return err
		}
		moved := r.allIn(files, r.completedBucket)
		if moved == nil {
			for _, f := range r.sentFiles() {
				if err := checkDownload(r.base, f, files[f.key].GetUrl()); err != nil {
					return err
				}
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not moved after %s: %w", mediaMoveWait, moved)
		}
		if !backoff.Pause(r.base, mediaMovePoll) {
			return r.base.Err()
		}
	}
}

func (r *mediaRun) allIn(files map[string]*chatimv1.FileURL, bucket string) error {
	for _, f := range r.sentFiles() {
		u := files[f.key]
		if u == nil {
			return fmt.Errorf("no URL for %s", f.name)
		}
		if err := inBucket(f.name, u.GetUrl(), bucket); err != nil {
			return err
		}
	}
	return nil
}

func (r *mediaRun) unsentStaysProcessing() error {
	a, err := r.client(r.user)
	if err != nil {
		return err
	}
	all, err := r.uploadKeys(a, nil, r.image.key, r.page.key, r.upload.GetKey())
	if err != nil {
		return err
	}
	wants := []string{r.completedBucket, r.completedBucket, r.processingBucket}
	for i, f := range all {
		if f.GetExpired() || f.GetUrl() == "" {
			return fmt.Errorf("upload %s expired %t with url %t, want a url and not expired", f.GetKey(), f.GetExpired(), f.GetUrl() != "")
		}
		if err := inBucket(f.GetKey(), f.GetUrl(), wants[i]); err != nil {
			return err
		}
	}
	return nil
}
