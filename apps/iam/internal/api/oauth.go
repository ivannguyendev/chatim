package api

import (
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const formContentType = "application/x-www-form-urlencoded"

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

var oauthDescriptions = map[string]string{
	"invalid_request":        "the request is missing or repeats a parameter, or is malformed",
	"unsupported_grant_type": "only refresh_token is supported",
	"invalid_grant":          "the refresh token is invalid, expired or revoked",
	"invalid_dpop_proof":     "the DPoP proof is missing or invalid",
	"unsupported_token_type": "only refresh tokens can be revoked",
	"server_error":           "temporarily unavailable, retry later",
}

func (s *server) token(w http.ResponseWriter, r *http.Request) {
	form, ok := s.readForm(w, r)
	if !ok {
		return
	}
	grant, okGrant := single(form, "grant_type")
	if !okGrant {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if grant != "refresh_token" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	refresh, okToken := single(form, "refresh_token")
	if !okToken {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	proofs := r.Header.Values("DPoP")
	if len(proofs) != 1 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_dpop_proof")
		return
	}
	proof, err := s.opts.Proofs.Verify(proofs[0], r.Method, r.URL.Path)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_dpop_proof")
		return
	}
	if len(form["client_id"]) > 1 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	clientID := form.Get("client_id")
	tok, err := s.opts.Auth.Refresh(r.Context(), auth.RefreshRequest{
		Tenant: r.PathValue("tenant"), RefreshToken: refresh, ClientID: clientID,
		IP: guard.ClientIP(r, s.opts.ClientIPHeader), Proof: auth.Proof{Thumbprint: proof.Thumbprint, JTI: proof.JTI},
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, tokenResponse{AccessToken: tok.AccessToken, TokenType: "Bearer", ExpiresIn: s.expiresIn(tok), RefreshToken: tok.RefreshToken})
	case errors.Is(err, auth.ErrProofReplayed):
		writeOAuthError(w, http.StatusBadRequest, "invalid_dpop_proof")
	case errors.Is(err, apperr.ErrUnauthenticated):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
	default:
		s.serverError(w, r, err)
	}
}

func (s *server) revoke(w http.ResponseWriter, r *http.Request) {
	form, ok := s.readForm(w, r)
	if !ok {
		return
	}
	token, okToken := single(form, "token")
	if !okToken {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	err := s.opts.Auth.Revoke(r.Context(), auth.RevokeRequest{Tenant: r.PathValue("tenant"), Token: token, IP: guard.ClientIP(r, s.opts.ClientIPHeader)})
	switch {
	case err == nil:
		noStore(w.Header())
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, auth.ErrUnsupportedTokenType):
		writeOAuthError(w, http.StatusBadRequest, "unsupported_token_type")
	default:
		s.serverError(w, r, err)
	}
}

func (s *server) readForm(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != formContentType {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	if err := r.ParseForm(); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			writeOAuthError(w, http.StatusRequestEntityTooLarge, "invalid_request")
			return nil, false
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	return r.PostForm, true
}

func (s *server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, apperr.ErrUnavailable) {
		s.opts.Log.ErrorContext(r.Context(), "iam oauth request failed", "path", r.URL.Path, "err", err.Error())
	}
	w.Header().Set("Retry-After", "1")
	writeOAuthError(w, http.StatusServiceUnavailable, "server_error")
}

func single(form url.Values, key string) (string, bool) {
	v := form[key]
	if len(v) != 1 || v[0] == "" {
		return "", false
	}
	return v[0], true
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, oauthError{Error: code, Description: oauthDescriptions[code]})
}
