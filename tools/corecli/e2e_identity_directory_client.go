package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	feedPath        = "/internal/v1/directory/changes"
	feedHeadPages   = 1000
	feedCallBudget  = 25 * time.Second
	feedWaitSeconds = 10
	readyPause      = 200 * time.Millisecond
)

type feedChange struct {
	Kind        string `json:"kind"`
	TenantID    string `json:"tenant_id"`
	ID          string `json:"id"`
	State       string `json:"state"`
	DisplayName string `json:"display_name"`
}

type feedReply struct {
	Changes []feedChange `json:"changes"`
	Next    string       `json:"next"`
}

type feedClient struct {
	internal string
	secret   string
	http     *http.Client
}

func newFeedClient(internal, secret string) *feedClient {
	transport := &http.Transport{MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1}
	return &feedClient{internal: strings.TrimSuffix(internal, "/"), secret: secret, http: &http.Client{Timeout: feedCallBudget, Transport: transport}}
}

func (d *feedClient) changes(ctx context.Context, secret, after string, wait int, wrote func()) (feedReply, int, error) {
	q := url.Values{"wait": {strconv.Itoa(wait)}}
	if after != "" {
		q.Set("after", after)
	}
	if wrote != nil {
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wrote() }})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.internal+feedPath+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return feedReply{}, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := d.http.Do(req)
	if err != nil {
		return feedReply{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return feedReply{}, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return feedReply{}, resp.StatusCode, nil
	}
	var out feedReply
	if err := json.Unmarshal(raw, &out); err != nil {
		return feedReply{}, resp.StatusCode, fmt.Errorf("decode feed page: %w", err)
	}
	return out, resp.StatusCode, nil
}

func (d *feedClient) mustChanges(ctx context.Context, after string, wait int, wrote func()) (feedReply, error) {
	page, status, err := d.changes(ctx, d.secret, after, wait, wrote)
	if err != nil {
		return feedReply{}, err
	}
	if status != http.StatusOK {
		return feedReply{}, fmt.Errorf("feed status %d, want 200", status)
	}
	return page, nil
}

func (d *feedClient) head(ctx context.Context) (string, error) {
	next := ""
	for range feedHeadPages {
		page, err := d.mustChanges(ctx, next, 0, nil)
		if err != nil {
			return "", err
		}
		if page.Next == next {
			return next, nil
		}
		next = page.Next
	}
	return "", fmt.Errorf("the feed still advanced after %d pages", feedHeadPages)
}

func (d *feedClient) awaitChange(ctx context.Context, after string, limit time.Duration, wrote func(), match func(feedChange) bool) (string, error) {
	deadline := time.Now().Add(limit)
	next := after
	for time.Now().Before(deadline) {
		page, err := d.mustChanges(ctx, next, feedWaitSeconds, wrote)
		if err != nil {
			return "", err
		}
		wrote = nil
		next = page.Next
		if slices.ContainsFunc(page.Changes, match) {
			return next, nil
		}
	}
	return "", errors.New("the feed did not return the change in time")
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
