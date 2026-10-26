package httpcors

import (
	"net/http"
	"slices"
	"strings"
)

const maxAge = "600"

type Config struct {
	Origins       []string
	AllowHeaders  []string
	ExposeHeaders []string
}

type handler struct {
	origins []string
	allow   string
	expose  string
	next    http.Handler
}

func New(cfg Config, next http.Handler) http.Handler {
	return handler{
		origins: slices.Clone(cfg.Origins),
		allow:   strings.Join(cfg.AllowHeaders, ", "),
		expose:  strings.Join(cfg.ExposeHeaders, ", "),
		next:    next,
	}
}

func (c handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	allowed := origin != "" && slices.Contains(c.origins, origin)
	preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
	if allowed && c.expose != "" {
		h.Set("Access-Control-Expose-Headers", c.expose)
	}
	if allowed {
		h.Set("Access-Control-Allow-Origin", origin)
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
	h.Set("Access-Control-Allow-Headers", c.allow)
	h.Set("Access-Control-Max-Age", maxAge)
	w.WriteHeader(http.StatusNoContent)
}
