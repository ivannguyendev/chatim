package main

import (
	"bytes"
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

	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
)

const (
	feedPath       = "/internal/v1/directory/changes"
	feedHeadPages  = 1000
	feedCallBudget = 25 * time.Second
)

type directoryUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
}

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

type directoryClient struct {
	iam      *iamClient
	internal string
	secret   string
	feed     *http.Client
}

func newDirectoryClient(c *iamClient, internal, secret string) *directoryClient {
	transport := &http.Transport{MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1}
	return &directoryClient{iam: c, internal: strings.TrimSuffix(internal, "/"), secret: secret, feed: &http.Client{Timeout: feedCallBudget, Transport: transport}}
}

func (d *directoryClient) getUsers(ctx context.Context, access string, ids []string) ([]directoryUser, []byte, error) {
	body, err := json.Marshal(map[string][]string{"userIds": ids})
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.iam.base+iamv1connect.DirectoryServiceGetUsersProcedure, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := d.iam.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, raw, fmt.Errorf("GetUsers status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Users []directoryUser `json:"users"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, fmt.Errorf("decode GetUsers: %w", err)
	}
	return out.Users, raw, nil
}

func (d *directoryClient) awaitUser(ctx context.Context, access, id string, limit time.Duration, match func(directoryUser) bool) (directoryUser, time.Duration, error) {
	started := time.Now()
	for {
		users, _, err := d.getUsers(ctx, access, []string{id})
		if err != nil {
			return directoryUser{}, 0, err
		}
		for _, u := range users {
			if u.ID == id && match(u) {
				return u, time.Since(started), nil
			}
		}
		if time.Since(started) > limit {
			return directoryUser{}, 0, fmt.Errorf("GetUsers(%s) = %+v after %v", id, users, limit)
		}
		select {
		case <-ctx.Done():
			return directoryUser{}, 0, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (d *directoryClient) changes(ctx context.Context, secret, after string, wait int, wrote func()) (feedReply, int, error) {
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
	resp, err := d.feed.Do(req)
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

func (d *directoryClient) mustChanges(ctx context.Context, after string, wait int, wrote func()) (feedReply, error) {
	page, status, err := d.changes(ctx, d.secret, after, wait, wrote)
	if err != nil {
		return feedReply{}, err
	}
	if status != http.StatusOK {
		return feedReply{}, fmt.Errorf("feed status %d, want 200", status)
	}
	return page, nil
}

func (d *directoryClient) head(ctx context.Context) (string, error) {
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

func (d *directoryClient) awaitChange(ctx context.Context, after string, limit time.Duration, wrote func(), match func(feedChange) bool) (string, error) {
	deadline := time.Now().Add(limit)
	next := after
	for time.Now().Before(deadline) {
		page, err := d.mustChanges(ctx, next, int(feedWaitSeconds), wrote)
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

func (d *directoryClient) readyz(ctx context.Context, adminURL string, limit time.Duration) (time.Duration, error) {
	started := time.Now()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(adminURL, "/")+"/readyz", http.NoBody)
		if err != nil {
			return 0, err
		}
		resp, err := d.iam.http.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(started), nil
			}
		}
		if time.Since(started) > limit {
			return 0, fmt.Errorf("iam /readyz not ready after %v", limit)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
