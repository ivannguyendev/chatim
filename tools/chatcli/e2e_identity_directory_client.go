package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	removedFeedPath = "/internal/v1/directory/changes"
	revocationsPath = "/internal/v1/revocations"
	readyPause      = 200 * time.Millisecond
)

func internalStatus(ctx context.Context, client *http.Client, internalURL, path, secret string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(internalURL, "/")+path, http.NoBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	return resp.StatusCode, nil
}

func expectInternalStatus(ctx context.Context, client *http.Client, internalURL, path, secret string, want int) error {
	status, err := internalStatus(ctx, client, internalURL, path, secret)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if status != want {
		return fmt.Errorf("GET %s = %d, want %d", path, status, want)
	}
	return nil
}

func awaitReady(ctx context.Context, client *http.Client, adminURL string, limit time.Duration) (time.Duration, error) {
	started := time.Now()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(adminURL, "/")+"/readyz", http.NoBody)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(started), nil
			}
		}
		if time.Since(started) > limit {
			return 0, fmt.Errorf("identity /readyz not ready after %v", limit)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(readyPause):
		}
	}
}
