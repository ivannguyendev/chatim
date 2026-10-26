package httpcors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/httpcors"
)

func newHandler() http.Handler {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	return httpcors.New(httpcors.Config{
		Origins:       []string{"https://app.example.com"},
		AllowHeaders:  []string{"Authorization", "DPoP"},
		ExposeHeaders: []string{"Grpc-Status"},
	}, next)
}

func serve(method, origin, requestMethod string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/x", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if requestMethod != "" {
		r.Header.Set("Access-Control-Request-Method", requestMethod)
	}
	w := httptest.NewRecorder()
	newHandler().ServeHTTP(w, r)
	return w
}

func TestAllowedOriginIsEchoedOnRequests(t *testing.T) {
	w := serve(http.MethodPost, "https://app.example.com", "")
	if w.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want the next handler", w.Code)
	}
	h := w.Header()
	if h.Get("Access-Control-Allow-Origin") != "https://app.example.com" || h.Get("Access-Control-Expose-Headers") != "Grpc-Status" {
		t.Fatalf("headers = %v", h)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must never be allowed")
	}
	if h.Values("Vary")[0] != "Origin" {
		t.Fatalf("Vary = %v, want Origin first", h.Values("Vary"))
	}
}

func TestUnknownOriginGetsNoCORSHeaders(t *testing.T) {
	for _, origin := range []string{"https://evil.example.com", "", "*"} {
		w := serve(http.MethodPost, origin, "")
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("origin %q was allowed", origin)
		}
	}
}

func TestPreflight(t *testing.T) {
	ok := serve(http.MethodOptions, "https://app.example.com", http.MethodPost)
	if ok.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status = %d, want 204", ok.Code)
	}
	h := ok.Header()
	if h.Get("Access-Control-Allow-Methods") != http.MethodPost || h.Get("Access-Control-Allow-Headers") != "Authorization, DPoP" || h.Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("preflight headers = %v", h)
	}
	if denied := serve(http.MethodOptions, "https://evil.example.com", http.MethodPost); denied.Code != http.StatusForbidden {
		t.Fatalf("denied preflight status = %d, want 403", denied.Code)
	}
	if plain := serve(http.MethodOptions, "https://app.example.com", ""); plain.Code != http.StatusTeapot {
		t.Fatalf("OPTIONS without request method = %d, want the next handler", plain.Code)
	}
}
