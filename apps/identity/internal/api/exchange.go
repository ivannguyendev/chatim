package api

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/guard"
)

const (
	exchangeGrantURN = "urn:ietf:params:oauth:grant-type:token-exchange"
	subjectTypeJWT   = "urn:ietf:params:oauth:token-type:jwt"
	issuedTypeAccess = "urn:ietf:params:oauth:token-type:access_token"
)

type Exchanger interface {
	Exchange(ctx context.Context, subjectToken, clientIP string) (exchange.Issued, error)
}

type ExchangeStats struct {
	OK       atomic.Uint64
	Invalid  atomic.Uint64
	Disabled atomic.Uint64
	Limited  atomic.Uint64
	Errors   atomic.Uint64
}

type exchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
}

func (s *server) token(w http.ResponseWriter, r *http.Request) {
	form, ok := readForm(w, r)
	if !ok {
		return
	}
	grant, ok := single(form, "grant_type")
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, oauthInvalidRequest)
		return
	}
	if grant != exchangeGrantURN {
		writeOAuthError(w, http.StatusBadRequest, oauthUnsupportedGrantType)
		return
	}
	ip := guard.ClientIP(r, s.opts.ClientIPHeader)
	if !s.opts.ExchangeIPs.Allow(ip, s.opts.Now()) {
		s.opts.ExchangeStats.Limited.Add(1)
		writeOAuthError(w, http.StatusTooManyRequests, oauthUnavailable)
		return
	}
	tokenType, okType := single(form, "subject_token_type")
	subjectToken, okToken := single(form, "subject_token")
	if !okType || tokenType != subjectTypeJWT || !okToken {
		writeOAuthError(w, http.StatusBadRequest, oauthInvalidRequest)
		return
	}
	issued, err := s.opts.Exchange.Exchange(r.Context(), subjectToken, ip)
	stats := s.opts.ExchangeStats
	switch {
	case err == nil:
		stats.OK.Add(1)
		writeSized(w, http.StatusOK, exchangeResponse{
			AccessToken: issued.AccessToken, IssuedTokenType: issuedTypeAccess, TokenType: "Bearer", ExpiresIn: s.expiresIn(issued.ExpiresAt),
		})
	case errors.Is(err, exchange.ErrRefused):
		stats.Invalid.Add(1)
		writeOAuthError(w, http.StatusBadRequest, oauthInvalidGrant)
	case errors.Is(err, exchange.ErrDisabled):
		stats.Disabled.Add(1)
		writeOAuthError(w, http.StatusBadRequest, oauthInvalidGrant)
	case errors.Is(err, context.Canceled):
		writeOAuthError(w, http.StatusServiceUnavailable, oauthUnavailable)
	default:
		stats.Errors.Add(1)
		writeOAuthError(w, http.StatusServiceUnavailable, oauthUnavailable)
	}
}

func (s *server) expiresIn(at time.Time) int64 {
	return max(int64((at.Sub(s.opts.Now())+time.Second-1)/time.Second), 0)
}
