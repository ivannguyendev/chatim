package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func readyzServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func TestProbeCommandExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   int
	}{
		{"ready", http.StatusOK, 0},
		{"not ready", http.StatusServiceUnavailable, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CORE_ADMIN_ADDR", readyzServer(t, tt.status))
			if got := Main([]string{"probe"}); got != tt.want {
				t.Fatalf("Main(probe) = %d, want %d", got, tt.want)
			}
		})
	}
}
