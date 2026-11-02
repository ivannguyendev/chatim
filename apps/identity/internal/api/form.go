package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
)

const formContentType = "application/x-www-form-urlencoded"

const (
	oauthInvalidRequest       = "invalid_request"
	oauthUnsupportedGrantType = "unsupported_grant_type"
	oauthInvalidGrant         = "invalid_grant"
	oauthUnavailable          = "temporarily_unavailable"
)

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

var oauthDescriptions = map[string]string{
	oauthInvalidRequest:       "the request is missing or repeats a parameter, or is malformed",
	oauthUnsupportedGrantType: "only urn:ietf:params:oauth:grant-type:token-exchange is supported",
	oauthInvalidGrant:         "the subject token is invalid, expired or not trusted",
	oauthUnavailable:          "temporarily unavailable, retry later",
}

func readForm(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != formContentType {
		writeOAuthError(w, http.StatusBadRequest, oauthInvalidRequest)
		return nil, false
	}
	if err := r.ParseForm(); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			writeOAuthError(w, http.StatusRequestEntityTooLarge, oauthInvalidRequest)
			return nil, false
		}
		writeOAuthError(w, http.StatusBadRequest, oauthInvalidRequest)
		return nil, false
	}
	return r.PostForm, true
}

func single(form url.Values, key string) (string, bool) {
	v := form[key]
	if len(v) != 1 || v[0] == "" {
		return "", false
	}
	return v[0], true
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	writeSized(w, status, oauthError{Error: code, Description: oauthDescriptions[code]})
}

func writeSized(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, oauthError{Error: "server_error"})
		return
	}
	h := w.Header()
	noStore(h)
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(raw)))
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
