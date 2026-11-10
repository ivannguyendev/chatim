package app_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

const (
	shopIssuer = "https://shop.example"
	shopKeyID  = "shop-1"
)

type trustFake struct {
	trustGate  chan struct{}
	trustLoads chan struct{}
	trustHung  chan struct{}
	trustDown  atomic.Bool
	trustHang  atomic.Bool
	lastTrust  atomic.Int64
	tenantKey  ed25519.PrivateKey
	profiles   atomic.Int64
	links      atomic.Int64
}

func (f *fakeStore) TrustedIssuers(ctx context.Context) ([]store.TrustedIssuer, error) {
	defer func() { f.lastTrust.Store(f.seq.Add(1)) }()
	signal(f.trustLoads)
	if f.trustGate != nil {
		select {
		case <-f.trustGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.trustHang.Load() {
		signal(f.trustHung)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.trustDown.Load() {
		return nil, errOpen
	}
	return []store.TrustedIssuer{{Issuer: shopIssuer, TenantID: "shop", IssuerState: store.StateActive, TenantState: store.StateActive}}, nil
}

func (f *fakeStore) TrustedKeys(context.Context) ([]store.TrustedKey, error) {
	if f.tenantKey == nil {
		return nil, nil
	}
	public, _ := f.tenantKey.Public().(ed25519.PublicKey)
	jwk, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
	if err != nil {
		return nil, err
	}
	return []store.TrustedKey{{Issuer: shopIssuer, KeyID: shopKeyID, Algorithm: "EdDSA", PublicKey: jwk, State: store.StateActive}}, nil
}

func (f *fakeStore) WriteProfile(context.Context, store.ProfileWrite) (bool, error) {
	f.profiles.Add(1)
	return true, nil
}

func (f *fakeStore) LinkUser(_ context.Context, u store.NewUser) (store.LinkedUser, error) {
	f.links.Add(1)
	return store.LinkedUser{ID: u.CandidateID, Result: store.LinkCreated}, nil
}

func (f *fakeStore) JITLinkBatchDocs() metrics.Histogram {
	return metrics.Histogram{Bounds: []float64{1, 2}, Cumulative: []uint64{2, 2}, Count: 2, Sum: 2}
}

func (f *fakeStore) JITUserBatchDocs() metrics.Histogram {
	return metrics.Histogram{Bounds: []float64{1, 2}, Cumulative: []uint64{1, 3}, Count: 3, Sum: 5}
}

func newTenantKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func tenantToken(t *testing.T, key ed25519.PrivateKey, sub string) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": shopIssuer, "sub": sub, "aud": "tenant-aud", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	})
	token.Header["kid"] = shopKeyID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func postExchange(t *testing.T, addr, subjectToken string) (int, string) {
	t.Helper()
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subjectToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/v1/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}
