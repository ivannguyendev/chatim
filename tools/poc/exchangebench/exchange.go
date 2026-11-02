package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
)

const (
	exchangeGrant  = "urn:ietf:params:oauth:grant-type:token-exchange"
	subjectTypeJWT = "urn:ietf:params:oauth:token-type:jwt"
	transportError = 0
	probeInterval  = time.Second
)

type exchangeClient struct {
	url  string
	http *http.Client
}

type bench struct {
	client *exchangeClient
	tokens []string
	stats  stats
}

func newExchangeClient(endpoint string, timeout time.Duration, conns int) *exchangeClient {
	transport := &http.Transport{
		MaxIdleConns:        conns,
		MaxIdleConnsPerHost: conns,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	return &exchangeClient{url: endpoint, http: &http.Client{Timeout: timeout, Transport: transport}}
}

func (c *exchangeClient) exchange(ctx context.Context, token string) int {
	form := url.Values{"grant_type": {exchangeGrant}, "subject_token": {token}, "subject_token_type": {subjectTypeJWT}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(form.Encode()))
	if err != nil {
		return transportError
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return transportError
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (c *exchangeClient) awaitReady(ctx context.Context, s signer, limit time.Duration) (time.Duration, error) {
	started := time.Now()
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	last := transportError
	for time.Since(started) < limit {
		token, err := s.sign(s.cfg.userPrefix+probeUser, time.Now())
		if err != nil {
			return 0, fmt.Errorf("sign probe token: %w", err)
		}
		if last = c.exchange(ctx, token); last == http.StatusOK {
			return time.Since(started), nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
	return 0, fmt.Errorf("probe exchange still answers %d after %v: is the issuer key registered?", last, limit)
}

func (b *bench) fire(ctx context.Context, s openloop.Shot) {
	code := b.client.exchange(ctx, b.tokens[s.Index])
	if s.Measured {
		b.stats.record(code, time.Since(s.Scheduled))
	}
}
