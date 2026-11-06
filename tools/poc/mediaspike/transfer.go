package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var errMissingBucket = errors.New("bucket does not exist")

var putClient = &http.Client{Transport: &http.Transport{ExpectContinueTimeout: 5 * time.Second}}

type putResult struct {
	status int
	etag   string
	header http.Header
}

func put(ctx context.Context, sp signedPart, body []byte, origin string) (putResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sp.url, bytes.NewReader(body))
	if err != nil {
		return putResult{}, err
	}
	req.ContentLength = int64(len(body))
	maps.Copy(req.Header, sp.header)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Expect", "100-continue")
	resp, err := putClient.Do(req)
	if err != nil {
		return putResult{}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return putResult{status: resp.StatusCode, etag: resp.Header.Get("ETag"), header: resp.Header}, nil
}

func preflight(ctx context.Context, rawURL, origin string) (http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Header, resp.StatusCode, nil
}

func withHost(rawURL, host string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Host = host
	return u.String()
}

func randomBytes(n int64) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func splitSizes(total, partSize int64) []int64 {
	var sizes []int64
	for left := total; left > 0; left -= partSize {
		sizes = append(sizes, min(left, partSize))
	}
	return sizes
}

func uploadAll(ctx context.Context, s objectStore, key, uploadID string, sizes []int64, chunk []byte, workers int) ([]part, error) {
	parts := make([]part, len(sizes))
	jobs := make(chan int)
	errs := make(chan error, len(sizes))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				n := int32(i + 1)
				sp, err := s.signPart(ctx, key, uploadID, n, sizes[i], 15*time.Minute)
				if err != nil {
					errs <- err
					continue
				}
				r, err := put(ctx, sp, chunk[:sizes[i]], "")
				if err != nil || r.status != http.StatusOK {
					errs <- fmt.Errorf("part %d: status %d: %w", n, r.status, err)
					continue
				}
				parts[i] = part{number: n, etag: r.etag}
			}
		})
	}
	for i := range sizes {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	return parts, <-errs
}
