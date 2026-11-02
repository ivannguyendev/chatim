package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/identity/internal/guard"
	"github.com/ivannguyendev/chatim/pkg/httpcors"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/identity/v1/identityv1connect"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type Keys interface {
	JWKS() ([]byte, bool)
}

type Options struct {
	Keys           Keys
	Directory      Directory
	Revocations    Revocations
	Claims         Claims
	DirectoryGate  *userlimit.Gate
	DirectoryUsers *userlimit.Users
	DirectoryStats *DirectoryStats
	Exchange       Exchanger
	ExchangeIPs    *guard.IPLimiter
	ExchangeStats  *ExchangeStats
	BodyMax        int64
	CORSOrigins    []string
	ClientIPHeader string
	Now            func() time.Time
	Log            *slog.Logger
}

type server struct {
	opts      Options
	directory limiter
}

func New(opts Options) (http.Handler, error) {
	switch {
	case opts.Keys == nil:
		return nil, errors.New("api: keys are required")
	case opts.BodyMax <= 0:
		return nil, errors.New("api: body max must be positive")
	case opts.Directory != nil && (opts.Revocations == nil || opts.Claims == nil || opts.DirectoryGate == nil || opts.DirectoryUsers == nil):
		return nil, errors.New("api: the directory needs revocations, claims, a gate and user limits")
	case opts.Exchange != nil && opts.ExchangeIPs == nil:
		return nil, errors.New("api: the token exchange needs an ip limiter")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.DirectoryStats == nil {
		opts.DirectoryStats = &DirectoryStats{}
	}
	if opts.ExchangeStats == nil {
		opts.ExchangeStats = &ExchangeStats{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	s := &server{
		opts:      opts,
		directory: limiter{gate: opts.DirectoryGate, users: opts.DirectoryUsers, userLimited: &opts.DirectoryStats.UserLimited, inflightRejected: &opts.DirectoryStats.InflightRejected},
	}
	mux := http.NewServeMux()
	if opts.Directory != nil {
		mux.Handle(identityv1connect.NewDirectoryServiceHandler(directoryService{s: s}, connect.WithReadMaxBytes(directoryBodyMax)))
	}
	if opts.Exchange != nil {
		mux.HandleFunc("POST /v1/token", s.token)
	}
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "not found"})
	})
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.bodyLimit(r.URL.Path))
		mux.ServeHTTP(w, r)
	})
	return httpcors.New(httpcors.Config{
		Origins:       opts.CORSOrigins,
		AllowHeaders:  []string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms"},
		ExposeHeaders: []string{"Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin", "Retry-After"},
	}, limited), nil
}

func (s *server) jwks(w http.ResponseWriter, _ *http.Request) {
	body, ok := s.opts.Keys.JWKS()
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable", "message": "no signing keys yet"})
		return
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(body)
}

func noStore(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	noStore(w.Header())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
