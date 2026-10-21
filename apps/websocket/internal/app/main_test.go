package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestMainRejectsUnknownCommands(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"serve", "extra"}, {"probe", "extra"}} {
		if got := Main(args); got != 2 {
			t.Errorf("Main(%v) = %d, want 2", args, got)
		}
	}
}

func TestMainServeFailsOnInvalidConfig(t *testing.T) {
	t.Setenv("WS_JWT_KEYS_FILE", "")
	if got := Main(nil); got != 1 {
		t.Fatalf("Main() = %d, want 1 without WS_JWT_KEYS_FILE", got)
	}
}

func TestProbeCommandExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		status, want int
	}{"ready": {http.StatusOK, 0}, "not ready": {http.StatusServiceUnavailable, 1}} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			t.Setenv("WS_ADMIN_ADDR", srv.Listener.Addr().String())
			if got := Main([]string{"probe"}); got != tc.want {
				t.Fatalf("Main(probe) = %d, want %d", got, tc.want)
			}
		})
	}
}
