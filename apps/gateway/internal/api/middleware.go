package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/limit"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

const (
	maxAuthorization = 8 << 10
	msgUnauthorized  = "missing or invalid token"
	msgTooMany       = "too many requests, retry later"
	msgBusy          = "gateway busy, retry later"
)

type Verifier interface {
	Verify(token string) (jwtverify.Claims, error)
}

type chain struct {
	verifier Verifier
	gate     *limit.Gate
	users    *limit.Users
	bodyMax  int64
	stats    *stats
	errors   errorWriter
	log      *slog.Logger
	authLog  *rate.Limiter
}

func (c *chain) gated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.gate.Enter() {
			c.stats.nodeInflight.Add(1)
			c.errors.write(w, r, connect.CodeUnavailable, msgBusy)
			return
		}
		defer c.gate.Leave()
		next.ServeHTTP(w, r)
	})
}

func (c *chain) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, reason := c.authenticate(r.Header)
		if reason != "" {
			label := c.stats.authFailed(reason)
			if c.authLog.Allow() {
				c.log.WarnContext(r.Context(), "gateway auth failed", "reason", label)
			}
			c.errors.write(w, r, connect.CodeUnauthenticated, msgUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
	})
}

func (c *chain) authenticate(h http.Header) (jwtverify.Claims, string) {
	token, reason := bearer(h)
	if reason != "" {
		return jwtverify.Claims{}, reason
	}
	claims, err := c.verifier.Verify(token)
	if err != nil {
		return jwtverify.Claims{}, string(jwtverify.ReasonOf(err))
	}
	return claims, ""
}

func bearer(h http.Header) (string, string) {
	values := h.Values("Authorization")
	switch {
	case len(values) == 0:
		return "", reasonMissing
	case len(values) > 1:
		return "", reasonRepeated
	case len(values[0]) > maxAuthorization:
		return "", reasonTooLong
	}
	for i := range len(values[0]) {
		if b := values[0][i]; b < ' ' || b > '~' {
			return "", reasonBadValue
		}
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return "", reasonBadValue
	}
	return token, ""
}

func (c *chain) limited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := ClaimsFrom(r.Context())
		release, err := c.users.Acquire(limit.Key{Tenant: claims.Tenant, User: claims.User})
		if err != nil {
			if errors.Is(err, limit.ErrInflight) {
				c.stats.userInflight.Add(1)
			} else {
				c.stats.rateLimited.Add(1)
			}
			c.errors.write(w, r, connect.CodeResourceExhausted, msgTooMany)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

func (c *chain) measured(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := &countingBody{ReadCloser: r.Body}
		r.Body = body
		next.ServeHTTP(w, r)
		if r.ContentLength > c.bodyMax || body.n > c.bodyMax {
			c.stats.bodyTooLarge.Add(1)
		}
	})
}

type countingBody struct {
	io.ReadCloser
	n int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	return n, err
}
