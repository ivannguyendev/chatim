package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/go-jose/go-jose/v4"

	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
)

type prover struct {
	key *ecdsa.PrivateKey
}

func newProver() (*prover, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &prover{key: key}, nil
}

func (p *prover) proof(method, target string) (string, error) {
	opts := (&jose.SignerOptions{EmbedJWK: true}).WithType("dpop+jwt")
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: p.key}, opts)
	if err != nil {
		return "", err
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	claims, _ := json.Marshal(map[string]any{"jti": hex.EncodeToString(jti), "htm": method, "htu": target, "iat": time.Now().Unix()})
	obj, err := signer.Sign(claims)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

type iamClient struct {
	base   string
	tenant string
	http   *http.Client
	rpc    iamv1connect.AuthServiceClient
	used   []string
}

type iamTokens struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	SessionID string `json:"-"`
}

type oauthReply struct {
	Status int
	Error  string
	Tokens iamTokens
}

func newIAMClient(base, tenant string, timeout time.Duration) *iamClient {
	h := &http.Client{Timeout: timeout}
	return &iamClient{base: strings.TrimSuffix(base, "/"), tenant: tenant, http: h, rpc: iamv1connect.NewAuthServiceClient(h, base)}
}

func (c *iamClient) remember(secrets ...string) {
	for _, s := range secrets {
		if s != "" {
			c.used = append(c.used, s)
		}
	}
}

func (c *iamClient) login(ctx context.Context, p *prover, username, password string) (iamTokens, error) {
	proof, err := p.proof(http.MethodPost, c.base+iamv1connect.AuthServiceLoginProcedure)
	if err != nil {
		return iamTokens{}, err
	}
	req := connect.NewRequest(&iamv1.LoginRequest{Tenant: c.tenant, Username: username, Password: password, DeviceName: "e2e"})
	req.Header().Set("DPoP", proof)
	c.remember(password, proof)
	resp, err := c.rpc.Login(ctx, req)
	if err != nil {
		return iamTokens{}, err
	}
	t := iamTokens{Access: resp.Msg.GetAccessToken(), Refresh: resp.Msg.GetRefreshToken(), SessionID: resp.Msg.GetSessionId()}
	c.remember(t.Access, t.Refresh)
	return t, nil
}

func (c *iamClient) changePassword(ctx context.Context, username, current, next string) error {
	c.remember(current, next)
	_, err := c.rpc.ChangePassword(ctx, connect.NewRequest(&iamv1.ChangePasswordRequest{Tenant: c.tenant, Username: username, CurrentPassword: current, NewPassword: next}))
	return err
}

func (c *iamClient) sessions(ctx context.Context, access string) ([]*iamv1.Session, error) {
	req := connect.NewRequest(&iamv1.ListSessionsRequest{})
	req.Header().Set("Authorization", "Bearer "+access)
	resp, err := c.rpc.ListSessions(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetSessions(), nil
}

func (c *iamClient) revokeSession(ctx context.Context, access, id string) error {
	req := connect.NewRequest(&iamv1.RevokeSessionRequest{SessionId: id})
	req.Header().Set("Authorization", "Bearer "+access)
	_, err := c.rpc.RevokeSession(ctx, req)
	return err
}

func (c *iamClient) refresh(ctx context.Context, p *prover, refresh string) (oauthReply, error) {
	target := c.base + "/realms/" + c.tenant + "/protocol/openid-connect/token"
	proof, err := p.proof(http.MethodPost, target)
	if err != nil {
		return oauthReply{}, err
	}
	c.remember(proof)
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"chatim-e2e"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthReply{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("DPoP", proof)
	resp, err := c.http.Do(req)
	if err != nil {
		return oauthReply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return oauthReply{}, err
	}
	out := oauthReply{Status: resp.StatusCode}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		out.Error = e.Error
		return out, nil
	}
	if err := json.Unmarshal(body, &out.Tokens); err != nil {
		return oauthReply{}, fmt.Errorf("decode token response: %w", err)
	}
	c.remember(out.Tokens.Access, out.Tokens.Refresh)
	return out, nil
}

func (c *iamClient) mustRefresh(ctx context.Context, p *prover, refresh, what string) (iamTokens, error) {
	r, err := c.refresh(ctx, p, refresh)
	if err != nil {
		return iamTokens{}, fmt.Errorf("%s: %w", what, err)
	}
	if r.Status != http.StatusOK {
		return iamTokens{}, fmt.Errorf("%s: status %d %s, want 200", what, r.Status, r.Error)
	}
	return r.Tokens, nil
}

func (c *iamClient) refused(ctx context.Context, p *prover, refresh, what string) error {
	r, err := c.refresh(ctx, p, refresh)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if r.Status != http.StatusBadRequest || r.Error != "invalid_grant" {
		return fmt.Errorf("%s: status %d %q, want 400 invalid_grant", what, r.Status, r.Error)
	}
	return nil
}

func metricValue(ctx context.Context, metricsURL, series string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, http.NoBody)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), series+" "); ok {
			return strconv.ParseFloat(value, 64)
		}
	}
	return 0, fmt.Errorf("series %s not found", series)
}
