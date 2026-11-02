package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const trustLimit = 15 * time.Second

type forgedCase struct {
	name string
	sign func() (string, error)
}

func (r exchangeRun) unsigned(method jwt.SigningMethod, secret any) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(method, jwt.MapClaims{
		"iss": r.c.issuer, "aud": r.c.audience, "sub": exchangeUser,
		"iat": now.Unix(), "exp": now.Add(tenantTokenTTL).Unix(),
	})
	token.Header["kid"] = r.k1.KeyID
	signed, err := token.SignedString(secret)
	r.c.used.remember(signed)
	return signed, err
}

func (r exchangeRun) signedWith(key devtoken.TenantKey, edit func(*devtoken.TenantClaims)) func() (string, error) {
	return func() (string, error) {
		claims := r.c.claims(exchangeUser, exchangeNewName)
		edit(&claims)
		return r.c.sign(key, claims)
	}
}

func (r exchangeRun) forgedCases() ([]forgedCase, error) {
	public, err := r.k1.PublicJWK()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return []forgedCase{
		{"alg none", func() (string, error) { return r.unsigned(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType) }},
		{"HS256 keyed with the public JWK of k1", func() (string, error) { return r.unsigned(jwt.SigningMethodHS256, public) }},
		{"kid of k1 signed RS256 by k2", r.signedWith(r.k2, func(c *devtoken.TenantClaims) { c.KeyID = r.k1.KeyID })},
		{"unknown kid", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.KeyID = "k9" })},
		{"unknown issuer", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.Issuer = r.c.issuer + "-other" })},
		{"wrong aud", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.Audience = r.c.audience + "-other" })},
		{"expired", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.IssuedAt = now.Add(-5 * time.Minute) })},
		{"lifetime of 11 minutes", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.TTL = 11 * time.Minute })},
		{"tid of another tenant", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.Tenant = "e2e" + randomHex(4) })},
		{"sub auth0|abc", r.signedWith(r.k1, func(c *devtoken.TenantClaims) { c.Subject = "auth0|abc" })},
	}, nil
}

func (r exchangeRun) forged(ctx context.Context) error {
	cases, err := r.forgedCases()
	if err != nil {
		return err
	}
	var first []byte
	for _, fc := range cases {
		token, err := fc.sign()
		if err != nil {
			return fmt.Errorf("%s: sign: %w", fc.name, err)
		}
		reply, err := r.c.refused(ctx, token)
		if err != nil {
			return fmt.Errorf("%s: %w", fc.name, err)
		}
		if first == nil {
			first = reply.Body
		}
		if !bytes.Equal(reply.Body, first) {
			return fmt.Errorf("%s: body %s differs from %s", fc.name, reply.Body, first)
		}
	}
	token, err := r.c.sign(r.k1, r.c.claims(exchangeUser, exchangeNewName))
	if err != nil {
		return err
	}
	reply, err := r.c.post(ctx, url.Values{"grant_type": {"client_credentials"}, "subject_token": {token}, "subject_token_type": {subjectTypeJWT}})
	if err != nil {
		return err
	}
	if reply.Status != http.StatusBadRequest || reply.Error != "unsupported_grant_type" {
		return fmt.Errorf("grant_type client_credentials = %d %q, want 400 unsupported_grant_type", reply.Status, reply.Error)
	}
	fmt.Printf("identity: %d forged or wrong tenant tokens got the same 400 invalid_grant body; a wrong grant_type got unsupported_grant_type\n", len(cases))
	return nil
}
