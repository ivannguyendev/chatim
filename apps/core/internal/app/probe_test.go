package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestProbeSucceedsWhenReady(t *testing.T) {
	if err := Probe(t.Context(), readyzServer(t, http.StatusOK)); err != nil {
		t.Fatalf("probe = %v, want nil", err)
	}
}

func TestProbeFailsWhenNotReady(t *testing.T) {
	if err := Probe(t.Context(), readyzServer(t, http.StatusServiceUnavailable)); err == nil {
		t.Fatal("probe = nil, want an error for 503")
	}
}

func TestProbeFailsWhenNothingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	if err := Probe(t.Context(), addr); err == nil {
		t.Fatal("probe = nil, want an error when the admin port is closed")
	}
}

func TestProbeGivesUpOnASlowServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if err := Probe(ctx, srv.Listener.Addr().String()); err == nil {
		t.Fatal("probe = nil, want a timeout")
	}
	if took := time.Since(begin); took > probeTimeout {
		t.Fatalf("probe took %v, want it bounded by its context", took)
	}
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

func TestLoopbackAddr(t *testing.T) {
	tests := []struct{ in, want string }{
		{":9090", "127.0.0.1:9090"},
		{"0.0.0.0:9090", "127.0.0.1:9090"},
		{"[::]:9090", "127.0.0.1:9090"},
		{"127.0.0.1:7090", "127.0.0.1:7090"},
		{"10.0.0.5:9090", "10.0.0.5:9090"},
		{"not-an-addr", "not-an-addr"},
	}
	for _, tt := range tests {
		if got := loopbackAddr(tt.in); got != tt.want {
			t.Errorf("loopbackAddr(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
