package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/dpop"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/httpcors"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type Auth interface {
	Login(ctx context.Context, req auth.LoginRequest) (auth.Tokens, error)
	ChangePassword(ctx context.Context, req auth.ChangePasswordRequest) error
	Refresh(ctx context.Context, req auth.RefreshRequest) (auth.Tokens, error)
	Revoke(ctx context.Context, req auth.RevokeRequest) error
	ListSessions(ctx context.Context, c auth.Caller) ([]auth.SessionView, error)
	RevokeSession(ctx context.Context, c auth.Caller, id string) error
	MyProfile(ctx context.Context, c auth.Caller) (store.Profile, error)
	UpdateMyProfile(ctx context.Context, c auth.Caller, u store.ProfileUpdate) (store.Profile, error)
}

type Proofs interface {
	Verify(proof, method, path string) (dpop.Proof, error)
}

type Callers interface {
	Caller(token string) (auth.Caller, error)
}

type Keys interface {
	JWKS() ([]byte, bool)
}

type Options struct {
	Auth           Auth
	Proofs         Proofs
	Callers        Callers
	Keys           Keys
	Directory      Directory
	Revocations    Revocations
	Claims         Claims
	DirectoryGate  *userlimit.Gate
	DirectoryUsers *userlimit.Users
	BodyMax        int64
	CORSOrigins    []string
	ClientIPHeader string
	Now            func() time.Time
	Log            *slog.Logger
}

type server struct {
	opts Options
}

func New(opts Options) (http.Handler, error) {
	switch {
	case opts.Auth == nil || opts.Proofs == nil || opts.Callers == nil || opts.Keys == nil:
		return nil, errors.New("api: auth, proofs, callers and keys are required")
	case opts.BodyMax <= 0:
		return nil, errors.New("api: body max must be positive")
	case opts.Directory != nil && (opts.Revocations == nil || opts.Claims == nil || opts.DirectoryGate == nil || opts.DirectoryUsers == nil):
		return nil, errors.New("api: the directory needs revocations, claims, a gate and user limits")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	s := &server{opts: opts}
	mux := http.NewServeMux()
	path, handler := iamv1connect.NewAuthServiceHandler(connectService{s: s}, connect.WithReadMaxBytes(int(opts.BodyMax)))
	mux.Handle(path, handler)
	if opts.Directory != nil {
		mux.Handle(iamv1connect.NewDirectoryServiceHandler(directoryService{s: s}, connect.WithReadMaxBytes(directoryBodyMax)))
	}
	mux.HandleFunc("POST /realms/{tenant}/protocol/openid-connect/token", s.token)
	mux.HandleFunc("POST /realms/{tenant}/protocol/openid-connect/revoke", s.revoke)
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
		AllowHeaders:  []string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "DPoP"},
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
