package api

import (
	"net/http"
	"slices"
)

const (
	corsAllowHeaders  = "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms"
	corsExposeHeaders = "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin"
	corsMaxAge        = "600"
)

type cors struct {
	origins []string
	next    http.Handler
}

func (c cors) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	allowed := origin != "" && slices.Contains(c.origins, origin)
	preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
	if allowed {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
	}
	if !preflight {
		c.next.ServeHTTP(w, r)
		return
	}
	h.Add("Vary", "Access-Control-Request-Method")
	h.Add("Vary", "Access-Control-Request-Headers")
	if !allowed {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	h.Set("Access-Control-Allow-Methods", http.MethodPost)
	h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
	h.Set("Access-Control-Max-Age", corsMaxAge)
	w.WriteHeader(http.StatusNoContent)
}
