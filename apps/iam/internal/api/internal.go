package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/revocation"
)

type RevocationSource interface {
	Revocations(ctx context.Context) (revocation.Snapshot, error)
}

type InternalStats struct {
	OK           atomic.Uint64
	NotModified  atomic.Uint64
	Unauthorized atomic.Uint64
	Errors       atomic.Uint64
}

type InternalOptions struct {
	Revocations RevocationSource
	Secret      string
	Stats       *InternalStats
	Log         *slog.Logger
}

type internal struct {
	opts   InternalOptions
	secret [sha256.Size]byte
}

func NewInternal(opts InternalOptions) (http.Handler, error) {
	if opts.Revocations == nil || opts.Secret == "" {
		return nil, errors.New("api: revocation source and node secret are required")
	}
	if opts.Stats == nil {
		opts.Stats = &InternalStats{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	in := &internal{opts: opts, secret: sha256.Sum256([]byte(opts.Secret))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/revocations", in.revocations)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "not found"})
	})
	return mux, nil
}

func (in *internal) revocations(w http.ResponseWriter, r *http.Request) {
	if !in.authorized(r.Header.Get("Authorization")) {
		in.opts.Stats.Unauthorized.Add(1)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthenticated", "message": "node secret required"})
		return
	}
	snap, err := in.opts.Revocations.Revocations(r.Context())
	if err != nil {
		in.opts.Stats.Errors.Add(1)
		in.opts.Log.WarnContext(r.Context(), "revocation list unavailable", "error", err)
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable", "message": "retry later"})
		return
	}
	body, err := json.Marshal(snap)
	if err != nil {
		in.opts.Stats.Errors.Add(1)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal", "message": "internal error"})
		return
	}
	sum := sha256.Sum256(body)
	tag := `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	noStore(w.Header())
	w.Header().Set("ETag", tag)
	if r.Header.Get("If-None-Match") == tag {
		in.opts.Stats.NotModified.Add(1)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	in.opts.Stats.OK.Add(1)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (in *internal) authorized(header string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(sum[:], in.secret[:]) == 1
}
