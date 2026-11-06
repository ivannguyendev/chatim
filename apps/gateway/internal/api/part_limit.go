package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/ratelimit"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type PartLimiter interface {
	Allow(ctx context.Context, k ratelimit.Key) (ratelimit.Decision, error)
}

func (c *chain) partLimited(next http.Handler) http.Handler {
	if c.parts == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != chatimv1.MediaService_GetPartURLs_FullMethodName {
			next.ServeHTTP(w, r)
			return
		}
		claims, _ := ClaimsFrom(r.Context())
		d, err := c.parts.Allow(r.Context(), ratelimit.Key{Tenant: claims.Tenant, User: claims.User, IP: c.clientIP.Of(r), Device: claims.Session})
		if err != nil && c.limitLog.Allow() {
			c.log.WarnContext(r.Context(), "part URL limit skipped, redis unavailable", "err", err)
		}
		if !d.Allowed {
			writeError(w, connect.CodeResourceExhausted, msgTooMany)
			return
		}
		next.ServeHTTP(w, r)
	})
}
