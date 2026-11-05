package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/pb/chatim/identity/v1/identityv1connect"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const (
	exchangeGrant    = "urn:ietf:params:oauth:grant-type:token-exchange"
	subjectTypeJWT   = "urn:ietf:params:oauth:token-type:jwt"
	issuedTypeAccess = "urn:ietf:params:oauth:token-type:access_token"
	chatTokenTTL     = 5 * time.Minute
	tenantTokenTTL   = 2 * time.Minute
	directoryLimit   = 2 * time.Second
	pollPause        = 100 * time.Millisecond
)

type exchangeReply struct {
	Status int
	Body   []byte
	Error  string
	Token  string
	Type   string
	Bearer string
	Expiry int64
}

type directoryUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
}

type identityClient struct {
	base     string
	issuer   string
	audience string
	tenant   string
	http     *http.Client
	used     *usedSecrets
}

func newIdentityClient(base, issuer, audience, tenant string, timeout time.Duration, used *usedSecrets) *identityClient {
	return &identityClient{
		base: strings.TrimSuffix(base, "/"), issuer: issuer, audience: audience, tenant: tenant,
		http: &http.Client{Timeout: timeout}, used: used,
	}
}

func (c *identityClient) claims(user, name string) devtoken.TenantClaims {
	return devtoken.TenantClaims{Issuer: c.issuer, Audience: c.audience, Subject: user, Name: name, TTL: tenantTokenTTL}
}

func (c *identityClient) sign(key devtoken.TenantKey, claims devtoken.TenantClaims) (string, error) {
	token, err := key.Sign(claims)
	c.used.remember(token)
	return token, err
}

func (c *identityClient) post(ctx context.Context, form url.Values) (exchangeReply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/token", strings.NewReader(form.Encode()))
	if err != nil {
		return exchangeReply{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return exchangeReply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchangeReply{}, err
	}
	var out struct {
		Error     string `json:"error"`
		Access    string `json:"access_token"`
		Type      string `json:"issued_token_type"`
		Bearer    string `json:"token_type"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return exchangeReply{}, fmt.Errorf("decode /v1/token status %d: %w", resp.StatusCode, err)
	}
	c.used.remember(out.Access)
	return exchangeReply{Status: resp.StatusCode, Body: body, Error: out.Error, Token: out.Access, Type: out.Type, Bearer: out.Bearer, Expiry: out.ExpiresIn}, nil
}

func (c *identityClient) exchange(ctx context.Context, subjectToken string) (exchangeReply, error) {
	return c.post(ctx, url.Values{"grant_type": {exchangeGrant}, "subject_token": {subjectToken}, "subject_token_type": {subjectTypeJWT}})
}

func (c *identityClient) mustExchange(ctx context.Context, key devtoken.TenantKey, claims devtoken.TenantClaims, what string) (string, error) {
	return c.awaitExchange(ctx, key, claims, 0, what)
}

func (c *identityClient) awaitExchange(ctx context.Context, key devtoken.TenantKey, claims devtoken.TenantClaims, limit time.Duration, what string) (string, error) {
	started := time.Now()
	for {
		token, err := c.sign(key, claims)
		if err != nil {
			return "", err
		}
		r, err := c.exchange(ctx, token)
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		if r.Status == http.StatusBadRequest && r.Error == "invalid_grant" && time.Since(started) < limit {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		return r.Token, c.checkIssued(r, claims.Subject, what)
	}
}

func (c *identityClient) checkIssued(r exchangeReply, user, what string) error {
	if r.Status != http.StatusOK || r.Token == "" {
		return fmt.Errorf("%s: status %d %q, want 200", what, r.Status, r.Error)
	}
	if r.Type != issuedTypeAccess || r.Bearer != "Bearer" || r.Expiry < int64(chatTokenTTL/time.Second)-1 || r.Expiry > int64(chatTokenTTL/time.Second) {
		return fmt.Errorf("%s: issued_token_type %q, token_type %q, expires_in %d", what, r.Type, r.Bearer, r.Expiry)
	}
	if err := checkChatToken(r.Token, c.tenant, user, chatTokenTTL); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func (c *identityClient) refused(ctx context.Context, token string) (exchangeReply, error) {
	r, err := c.exchange(ctx, token)
	if err != nil {
		return r, err
	}
	if r.Status != http.StatusBadRequest || r.Error != "invalid_grant" {
		return r, fmt.Errorf("status %d %q, want 400 invalid_grant", r.Status, r.Error)
	}
	return r, nil
}

func (c *identityClient) awaitRefused(ctx context.Context, key devtoken.TenantKey, claims devtoken.TenantClaims, limit, pause time.Duration) (time.Duration, error) {
	started := time.Now()
	for {
		token, err := c.sign(key, claims)
		if err != nil {
			return 0, err
		}
		r, err := c.exchange(ctx, token)
		if err != nil {
			return 0, err
		}
		switch {
		case r.Status == http.StatusBadRequest && r.Error == "invalid_grant":
			return time.Since(started), nil
		case r.Status != http.StatusOK:
			return 0, fmt.Errorf("status %d %q, want 200 then 400 invalid_grant", r.Status, r.Error)
		case time.Since(started) > limit:
			return 0, fmt.Errorf("still exchanged after %v", limit)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(pause):
		}
	}
}

func (c *identityClient) getUsers(ctx context.Context, access string, ids []string) ([]directoryUser, []byte, error) {
	body, err := json.Marshal(map[string][]string{"userIds": ids})
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+identityv1connect.DirectoryServiceGetUsersProcedure, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.http.Do(req)
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

func (c *identityClient) awaitUser(ctx context.Context, access, id string, limit time.Duration, match func(directoryUser) bool) (time.Duration, error) {
	started := time.Now()
	for {
		users, _, err := c.getUsers(ctx, access, []string{id})
		if err != nil {
			return 0, err
		}
		for _, u := range users {
			if u.ID == id && match(u) {
				return time.Since(started), nil
			}
		}
		if time.Since(started) > limit {
			return 0, fmt.Errorf("GetUsers(%s) = %+v after %v", id, users, limit)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
